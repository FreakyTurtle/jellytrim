// Command demo runs JellyTrim's real web UI on an invented library, for
// README screenshots and UI work. It starts a fake Jellyfin in-process,
// makes sparse media files (no data, no disk space) under -dir, answers
// ffprobe from the test fixtures, and seeds a config as if setup had been
// completed, with past jobs in History and jobs waiting in the Queue.
//
// It never runs ffmpeg or ffprobe and never touches files outside -dir.
// Dry Run is on unless -running is given, and the queue's workers never
// start, so nothing is ever encoded.
//
// Development only. Usage:
//
//	go run ./scripts/demo [-listen 127.0.0.1:8098] [-dir ./tmp/demo] [-reset] [-running]
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/freakyturtle/jellytrim/internal/encoder"
	"github.com/freakyturtle/jellytrim/internal/library"
	"github.com/freakyturtle/jellytrim/internal/queue"
	"github.com/freakyturtle/jellytrim/internal/store"
	"github.com/freakyturtle/jellytrim/internal/web"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "demo:", err)
		os.Exit(1)
	}
}

func run() error {
	listen := flag.String("listen", "127.0.0.1:8098", "address to serve the web UI on")
	dir := flag.String("dir", "./tmp/demo", "folder for the demo's config and sparse media files")
	reset := flag.Bool("reset", false, "delete the demo's config and media and build them again")
	running := flag.Bool("running", false, "show one job encoding (turns Dry Run off and keeps weekday working hours free; nothing really runs)")
	verbose := flag.Bool("v", false, "log every request")
	flag.Parse()

	level := slog.LevelWarn
	if *verbose {
		level = slog.LevelInfo
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	tb := &demoTB{}
	defer tb.close()
	d, err := build(ctx, tb, options{dir: *dir, reset: *reset, running: *running, now: time.Now, log: log})
	if err != nil {
		return err
	}
	defer d.close()
	return d.serve(ctx, *listen)
}

type options struct {
	dir     string
	reset   bool
	running bool
	now     func() time.Time
	log     *slog.Logger
}

// demo holds the services, wired as internal/app wires them, but with a
// fake Jellyfin, a fake prober and fake encoder test results.
type demo struct {
	dir   demoFolder
	now   func() time.Time
	cat   *catalogue
	fx    *fixtures
	probe *prober
	jf    *fakeJellyfin
	st    *store.Store
	reg   *encoder.Registry
	hw    *hardware
	lib   *library.Service
	queue *queue.Service
	log   *slog.Logger
	fresh bool
}

// build prepares the folder, the fake Jellyfin and the database. A new
// folder (or -reset) is seeded from scratch; an existing one is reused,
// keeping any changes made in the UI.
func build(ctx context.Context, tb testing.TB, o options) (*demo, error) {
	if err := checkWizardPolicies(); err != nil {
		return nil, err
	}
	dir, err := openFolder(o.dir, o.reset)
	if err != nil {
		return nil, err
	}
	d := &demo{dir: dir, now: o.now, log: o.log, probe: newProber()}
	if d.cat, err = newCatalogue(o.now()); err != nil {
		return nil, err
	}
	if d.fx, err = openFixtures(); err != nil {
		return nil, err
	}
	if err := d.prepareMedia(); err != nil {
		d.close()
		return nil, err
	}
	d.jf = startJellyfin(tb, d.cat, func(e entry) int64 { return fileSize(e.Local(dir.media())) })
	if err := d.open(ctx); err != nil {
		d.close()
		return nil, err
	}
	if err := d.seed(ctx, o.running); err != nil {
		d.close()
		return nil, err
	}
	return d, nil
}

// prepareMedia makes any missing sparse files and registers each file's
// probe.
func (d *demo) prepareMedia() error {
	for _, e := range d.cat.entries {
		local := e.Local(d.dir.media())
		if err := d.dir.sparseFile(local, e.Size, e.Added); err != nil {
			return err
		}
		if e.Linked {
			if err := d.dir.hardLink(local); err != nil {
				return err
			}
		}
		probe, frames, err := d.fx.sourceProbe(e, local)
		if err != nil {
			return fmt.Errorf("building the probe for %q: %w", e.Key, err)
		}
		d.probe.add(local, probe, frames)
	}
	return nil
}

// open opens the database and builds the services.
func (d *demo) open(ctx context.Context) error {
	cfg := d.dir.config()
	_, err := os.Stat(filepath.Join(cfg, store.FileName))
	d.fresh = errors.Is(err, os.ErrNotExist)
	if err := os.MkdirAll(cfg, 0o750); err != nil {
		return err
	}
	if d.st, err = store.Open(ctx, cfg); err != nil {
		return err
	}
	d.st.SetClock(d.now)
	d.reg = encoder.NewRegistry(encoder.DefaultBackends(encoder.DefaultQSVDevice))
	d.hw = &hardware{reg: d.reg, store: d.st, now: d.now}
	if err := d.hw.apply(ctx); err != nil {
		return err
	}
	d.lib = library.New(library.Options{Store: d.st, Log: d.log, Prober: d.probe, Encoders: d.reg, Now: d.now})
	// No Runner: the queue's workers are never started, and without a
	// runner nothing could encode even if they were.
	d.queue = queue.New(queue.Options{Store: d.st, Library: d.lib, Registry: d.reg, Log: d.log, Now: d.now, Space: fakeSpace{}})
	return nil
}

// seed fills a new database, or reconnects a reused one to this run's fake
// Jellyfin, then syncs.
func (d *demo) seed(ctx context.Context, running bool) error {
	if d.fresh {
		if err := d.seedFresh(ctx); err != nil {
			return err
		}
	} else {
		if _, err := d.lib.SaveConnection(ctx, d.jf.URL, ""); err != nil {
			return fmt.Errorf("reconnecting to the fake Jellyfin: %w", err)
		}
		if err := d.registerOutputs(ctx); err != nil {
			return err
		}
		if _, err := d.lib.Run(ctx); err != nil {
			return err
		}
	}
	if err := d.applyMode(ctx, running); err != nil {
		return err
	}
	// Put back any job an earlier start showed running, so a restart shows
	// it started just now and elapsed time matches its progress.
	if err := d.stopRunning(ctx); err != nil {
		return err
	}
	if running {
		return d.startRunning(ctx)
	}
	return nil
}

func (d *demo) seedFresh(ctx context.Context) error {
	if err := d.seedConfig(ctx); err != nil {
		return err
	}
	if _, err := d.lib.Run(ctx); err != nil {
		return err
	}
	// Queueing refuses in Dry Run.
	if err := d.st.SetSetting(ctx, store.KeyDryRun, "false"); err != nil {
		return err
	}
	if err := d.seedHistory(ctx); err != nil {
		return err
	}
	if err := d.registerOutputs(ctx); err != nil {
		return err
	}
	// Pick up the replaced files, as the sync after a job does.
	if _, err := d.lib.Run(ctx); err != nil {
		return err
	}
	return d.seedQueue(ctx)
}

// serve runs the real web UI until ctx ends.
func (d *demo) serve(ctx context.Context, addr string) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", addr, err)
	}
	d.lib.Start(ctx)
	srv := &http.Server{Handler: d.handler(), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	d.printSummary(ctx, "http://"+ln.Addr().String())
	select {
	case <-ctx.Done():
	case err := <-errc:
		return err
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
	d.lib.Wait()
	return nil
}

// handler is the app's web server over the demo's services.
func (d *demo) handler() http.Handler {
	return web.New(web.Deps{Store: d.st, Library: d.lib, Hardware: d.hw, Queue: d.queue, Log: d.log, Now: d.now}).Handler()
}

func (d *demo) printSummary(ctx context.Context, url string) {
	how := "Reused"
	if d.fresh {
		how = "Built"
	}
	tot, _ := d.st.Totals(ctx)
	fmt.Printf("JellyTrim demo: %s\n", url)
	fmt.Printf("%s %s: %d items, sparse media files (no real data).\n", how, d.dir.root, tot.Items)
	fmt.Println("Everything shown is invented: titles, people and history. Nothing is encoded. Ctrl-C to stop.")
}

func (d *demo) close() {
	if d.st != nil {
		_ = d.st.Close()
	}
	if d.fx != nil {
		d.fx.close()
	}
}

// fakeSpace reports a roomy media disk, so the Queue's space outlook shows
// the demo's numbers rather than this machine's.
type fakeSpace struct{}

func (fakeSpace) Free(string) (uint64, error)   { return uint64(freeSpaceTB * 1e12), nil }
func (fakeSpace) Device(string) (uint64, error) { return 1, nil }
