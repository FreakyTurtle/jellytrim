// Command jellytrim runs the JellyTrim server.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	_ "time/tzdata" // lets TZ work in minimal images without system zone files

	"github.com/freakyturtle/jellytrim/internal/app"
	"github.com/freakyturtle/jellytrim/internal/config"
	"github.com/freakyturtle/jellytrim/internal/version"
)

func main() {
	os.Exit(run())
}

func run() int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "jellytrim: configuration error:", err)
		return 2
	}
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		return healthcheck(cfg.Listen)
	}
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.StringVar(&cfg.Listen, "listen", cfg.Listen, "address to listen on (JELLYTRIM_LISTEN)")
	flag.StringVar(&cfg.ConfigDir, "config-dir", cfg.ConfigDir, "directory for the database (JELLYTRIM_CONFIG_DIR)")
	flag.Parse()

	if *showVersion {
		fmt.Println(version.String())
		return 0
	}

	log := newLogger(cfg)
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a, err := app.New(ctx, cfg, log)
	if err != nil {
		log.Error("jellytrim: cannot start", "err", err)
		return 1
	}
	if err := a.Run(ctx); err != nil {
		log.Error("jellytrim: stopped with an error", "err", err)
		return 1
	}
	return 0
}

func newLogger(cfg config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.LogJSON {
		return slog.New(slog.NewJSONHandler(os.Stderr, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stderr, opts))
}
