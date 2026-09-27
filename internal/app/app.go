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
	"time"

	"github.com/freakyturtle/jellytrim/internal/config"
	"github.com/freakyturtle/jellytrim/internal/store"
	"github.com/freakyturtle/jellytrim/internal/version"
	"github.com/freakyturtle/jellytrim/internal/web"
)

// ShutdownTimeout bounds how long a graceful shutdown may take.
const ShutdownTimeout = 10 * time.Second

// App is a running JellyTrim instance.
type App struct {
	cfg   config.Config
	log   *slog.Logger
	store *store.Store
	http  *http.Server
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
	srv := web.New(web.Deps{Store: st, Log: log})
	return &App{
		cfg:   cfg,
		log:   log,
		store: st,
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

// Run serves until ctx is cancelled, then shuts down in order.
func (a *App) Run(ctx context.Context) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", a.cfg.Listen)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", a.cfg.Listen, err)
	}
	a.log.Info("jellytrim: started", "version", version.Version, "listen", ln.Addr().String(), "config_dir", a.cfg.ConfigDir)

	errc := make(chan error, 1)
	go func() {
		if err := a.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
		close(errc)
	}()

	select {
	case <-ctx.Done():
	case err := <-errc:
		if err != nil {
			return fmt.Errorf("http server: %w", err)
		}
	}
	return a.shutdown()
}

func (a *App) shutdown() error {
	a.log.Info("jellytrim: shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), ShutdownTimeout)
	defer cancel()
	var errs []error
	if err := a.http.Shutdown(ctx); err != nil {
		errs = append(errs, fmt.Errorf("http shutdown: %w", err))
	}
	if err := a.store.Close(); err != nil {
		errs = append(errs, fmt.Errorf("closing database: %w", err))
	}
	a.log.Info("jellytrim: stopped")
	return errors.Join(errs...)
}
