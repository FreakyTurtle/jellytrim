// Package app wires JellyTrim's services together and runs them until the
// context is cancelled.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/freakyturtle/jellytrim/internal/config"
	"github.com/freakyturtle/jellytrim/internal/encoder"
	"github.com/freakyturtle/jellytrim/internal/ffmpeg"
	"github.com/freakyturtle/jellytrim/internal/library"
	"github.com/freakyturtle/jellytrim/internal/queue"
	"github.com/freakyturtle/jellytrim/internal/scheduler"
	"github.com/freakyturtle/jellytrim/internal/store"
	"github.com/freakyturtle/jellytrim/internal/version"
	"github.com/freakyturtle/jellytrim/internal/web"
)

// ShutdownTimeout bounds how long a graceful shutdown may take.
const ShutdownTimeout = 10 * time.Second

// App is a running JellyTrim instance.
type App struct {
	cfg       config.Config
	log       *slog.Logger
	store     *store.Store
	library   *library.Service
	queue     *queue.Service
	hardware  *hardware
	scheduler *scheduler.Scheduler
	http      *http.Server

	// bg tracks the scheduler and the start-up hardware test, so shutdown
	// waits for them before closing the store.
	bg sync.WaitGroup
}

// New opens the database and builds every service.
func New(ctx context.Context, cfg config.Config, log *slog.Logger) (*App, error) {
	if err := cfg.EnsureConfigDir(); err != nil {
		return nil, err
	}
	st, err := store.Open(ctx, cfg.ConfigDir)
	if err != nil {
		return nil, err
	}
	if err := applyPresets(ctx, st, cfg); err != nil {
		_ = st.Close()
		return nil, err
	}
	if err := st.MarkInterruptedSyncs(ctx); err != nil {
		_ = st.Close()
		return nil, err
	}
	runner := ffmpeg.New(cfg.FFmpeg, cfg.FFprobe, log)
	registry := encoder.NewRegistry(encoder.DefaultBackends(encoder.DefaultQSVDevice))
	lib := library.New(library.Options{Store: st, Log: log, Prober: runner, Encoders: registry})
	hw := &hardware{registry: registry, runner: runner, store: st, library: lib, log: log}
	hw.load(ctx)
	if s, err := st.Settings(ctx); err == nil {
		registry.SetPreferSoftware(s.EncoderPreference == "software")
	}
	q := queue.New(queue.Options{Store: st, Library: lib, Registry: registry, Runner: runner, Log: log})
	sched := &scheduler.Scheduler{Store: st, Library: lib, Queue: q, Log: log}
	srv := web.New(web.Deps{Store: st, Library: lib, Hardware: hw, Queue: q, Log: log})
	return &App{
		cfg:       cfg,
		log:       log,
		store:     st,
		library:   lib,
		queue:     q,
		hardware:  hw,
		scheduler: sched,
		http: &http.Server{
			Addr:              cfg.Listen,
			Handler:           srv.Handler(),
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       120 * time.Second,
		},
	}, nil
}

// applyPresets stores a Jellyfin connection given through the environment,
// but only if none is saved yet, so the UI stays the source of truth.
func applyPresets(ctx context.Context, st *store.Store, cfg config.Config) error {
	if cfg.JellyfinURL == "" && cfg.JellyfinAPIKey == "" {
		return nil
	}
	s, err := st.Settings(ctx)
	if err != nil {
		return err
	}
	kv := map[string]string{}
	if s.JellyfinURL == "" && cfg.JellyfinURL != "" {
		kv[store.KeyJellyfinURL] = cfg.JellyfinURL
	}
	if !s.HasAPIKey && cfg.JellyfinAPIKey != "" {
		kv[store.KeyJellyfinAPIKey] = cfg.JellyfinAPIKey
	}
	if len(kv) == 0 {
		return nil
	}
	return st.SetSettings(ctx, kv)
}

// Run serves until ctx is cancelled or the HTTP server fails, then shuts
// down in order. Every service runs on a context derived from ctx, so a
// server failure stops them too.
func (a *App) Run(parent context.Context) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(parent, "tcp", a.cfg.Listen)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", a.cfg.Listen, err)
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	a.library.Start(ctx)
	// Queueing and encoding wait for the hardware test and a fresh
	// evaluation: before that, every plan would say "no encoder" and the
	// jobs would be skipped. The web UI is available meanwhile.
	a.bg.Go(func() {
		if err := a.hardware.Retest(ctx); err != nil && ctx.Err() == nil {
			a.log.Warn("hardware: test failed", "err", err)
		}
		if ctx.Err() != nil {
			return
		}
		if _, err := a.library.Evaluate(ctx); err != nil && !errors.Is(err, library.ErrBusy) && ctx.Err() == nil {
			a.log.Warn("library: evaluating after the hardware test", "err", err)
		}
		a.queue.Start(ctx)
		a.scheduler.Run(ctx)
	})
	a.log.Info("jellytrim: started", "version", version.Version, "listen", ln.Addr().String(), "config_dir", a.cfg.ConfigDir)

	errc := make(chan error, 1)
	go func() {
		if err := a.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
		close(errc)
	}()

	var serveErr error
	select {
	case <-ctx.Done():
	case err := <-errc:
		if err != nil {
			serveErr = fmt.Errorf("http server: %w", err)
		}
	}
	cancel()
	return errors.Join(serveErr, a.shutdown())
}

// shutdown stops work in order, once the run context is cancelled: first
// the HTTP server (no new requests start work), then the scheduler and the
// hardware test, then the queue (running encodes stop, their partial files
// are removed and the jobs go back to waiting), then background library
// runs, and only then the database.
func (a *App) shutdown() error {
	a.log.Info("jellytrim: shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), ShutdownTimeout)
	defer cancel()
	var errs []error
	if err := a.http.Shutdown(ctx); err != nil {
		errs = append(errs, fmt.Errorf("http shutdown: %w", err))
	}
	a.bg.Wait()
	a.queue.Stop()
	a.library.Wait()
	if err := a.store.Close(); err != nil {
		errs = append(errs, fmt.Errorf("closing database: %w", err))
	}
	a.log.Info("jellytrim: stopped")
	return errors.Join(errs...)
}
