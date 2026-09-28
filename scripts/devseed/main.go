// Command devseed prepares a local JellyTrim config directory against the
// dev stack's Jellyfin, as if the setup wizard had been completed: it saves
// the connection, manages every library, selects the dev user, maps the
// Jellyfin paths to dev/media, adds the starter policies and runs a sync.
//
// Development only. Usage:
//
//	go run ./scripts/devseed -config tmp/config
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/freakyturtle/jellytrim/internal/ffmpeg"
	"github.com/freakyturtle/jellytrim/internal/library"
	"github.com/freakyturtle/jellytrim/internal/media"
	"github.com/freakyturtle/jellytrim/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "devseed:", err)
		os.Exit(1)
	}
}

func run() error {
	configDir := flag.String("config", "tmp/config", "JellyTrim config directory to prepare")
	jf := flag.String("jellyfin", "http://localhost:8096", "dev Jellyfin URL")
	keyFile := flag.String("key-file", "dev/jellyfin-api-key", "file holding the dev API key")
	mediaDir := flag.String("media", "dev/media", "local folder holding the fixture media")
	enableAll := flag.Bool("enable-all", false, "enable every starter policy, not just Protect favourites")
	live := flag.Bool("live", false, "turn Dry Run off, so JellyTrim will really optimise the fixture files")
	assumeX265 := flag.Bool("assume-x265", true, "treat software x265 as tested, so plans can be made before the hardware test runs")
	flag.Parse()

	ctx := context.Background()
	key, err := os.ReadFile(*keyFile)
	if err != nil {
		return fmt.Errorf("reading the API key (run task dev:bootstrap first): %w", err)
	}
	mediaRoot, err := filepath.Abs(*mediaDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*configDir, 0o750); err != nil {
		return err
	}
	st, err := store.Open(ctx, *configDir)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	opts := library.Options{Store: st, Log: log, Prober: ffmpeg.New("ffmpeg", "ffprobe", log)}
	if *assumeX265 {
		opts.Encoders = x265Only{}
	}
	lib := library.New(opts)

	info, err := lib.SaveConnection(ctx, *jf, strings.TrimSpace(string(key)))
	if err != nil {
		return fmt.Errorf("connecting to %s: %w", *jf, err)
	}
	if err := lib.RefreshServerInfo(ctx); err != nil {
		return err
	}
	if err := seedSelections(ctx, st, mediaRoot); err != nil {
		return err
	}
	if err := lib.CreateStarterPolicies(ctx, nil); err != nil {
		return err
	}
	if *enableAll {
		rows, _ := st.Policies(ctx)
		for _, p := range rows {
			_ = st.SetPolicyEnabled(ctx, p.ID, true)
		}
	}
	if *live {
		if err := st.SetSetting(ctx, store.KeyDryRun, "false"); err != nil {
			return err
		}
	}
	if err := st.SetSetting(ctx, store.KeySetupComplete, "true"); err != nil {
		return err
	}
	res, err := lib.Run(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("devseed: connected to %s, %d items synced, %d probed (%d failed), config in %s\n",
		info.ServerName, res.Items, res.Probed, res.Failures, *configDir)
	return nil
}

// x265Only stands in for the encoder registry in development seeding.
type x265Only struct{}

func (x265Only) Select(c media.Codec, _ bool, _ string) (string, bool, string) {
	if c == media.CodecHEVC {
		return "x265", true, ""
	}
	return "", false, "Only software HEVC is assumed in dev seeding."
}

func seedSelections(ctx context.Context, st *store.Store, mediaRoot string) error {
	libs, err := st.Libraries(ctx)
	if err != nil {
		return err
	}
	var ids []string
	var maps []store.PathMapping
	for _, l := range libs {
		ids = append(ids, l.ID)
		for _, loc := range l.Locations {
			maps = append(maps, store.PathMapping{JellyfinPrefix: loc, LocalPrefix: filepath.ToSlash(filepath.Join(mediaRoot, filepath.Base(loc)))})
		}
	}
	if err := st.SetManagedLibraries(ctx, ids); err != nil {
		return err
	}
	if err := st.ReplacePathMappings(ctx, maps); err != nil {
		return err
	}
	users, err := st.Users(ctx)
	if err != nil {
		return err
	}
	var sel []string
	for _, u := range users {
		if !u.Disabled {
			sel = append(sel, u.ID)
		}
	}
	return st.SetSelectedUsers(ctx, sel)
}
