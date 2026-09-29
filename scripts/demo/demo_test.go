package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/freakyturtle/jellytrim/internal/plan"
	"github.com/freakyturtle/jellytrim/internal/store"
)

// The demo is built from real services, so this test keeps it working as
// they change: every table entry must still be chosen by the policies, and
// every page must render.

func demoNow() time.Time { return time.Date(2026, 9, 29, 14, 0, 0, 0, time.UTC) }

func buildDemo(t *testing.T, dir string, running bool) *demo {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	d, err := build(context.Background(), t, options{dir: dir, running: running, now: demoNow, log: log})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.close)
	return d
}

func get(t *testing.T, base, path string) string {
	t.Helper()
	resp, err := http.Get(base + path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %d", path, resp.StatusCode)
	}
	return string(b)
}

func TestDemoSeedsEveryPage(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "demo")
	d := buildDemo(t, dir, false)
	srv := httptest.NewServer(d.handler())
	defer srv.Close()
	item := func(key string) string { return "/library/" + d.cat.byKey[key].ID }
	pages := []struct {
		path string
		want []string
	}{
		{"/", []string{"JellyTrim saved", "Dry Run"}},
		{"/library", []string{"Twelve Pale Lanterns", "Harbour Lights"}},
		{item("Twelve Pale Lanterns"), []string{"Needs optimisation", "Archive watched 4K"}},
		{item("Signal Box"), []string{"Dolby Vision without an HDR10 base layer"}},
		{item("Nine Bells for Hollin"), []string{"hard links"}},
		{item("The Long Acre S01E04"), []string{"interlaced"}},
		{"/policies", []string{"Archive watched 4K", "Efficient encoding"}},
		{"/policies/4", []string{"Would optimise"}},
		{"/queue", []string{"Twelve Pale Lanterns", "8 jobs waiting"}},
		{"/history", []string{"The Lantern Keeper", "Restored", "below the 10% minimum"}},
		{"/settings", []string{"No Intel GPU device", "Home Media"}},
	}
	for _, p := range pages {
		body := get(t, srv.URL, p.path)
		for _, w := range p.want {
			if !strings.Contains(body, w) {
				t.Errorf("GET %s does not mention %q", p.path, w)
			}
		}
	}

	ctx := context.Background()
	jobs, err := d.st.HistoryJobs(ctx, "", 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != len(history) {
		t.Errorf("History has %d jobs, want %d", len(jobs), len(history))
	}
	for _, j := range jobs {
		if j.Status == store.JobComplete && !strings.Contains(j.Diagnostics, `"pass":true`) {
			t.Errorf("%s: complete without passed checks", j.ItemName)
		}
	}
	ev, err := d.st.Evaluation(ctx, d.cat.byKey["The Glass Orchard"].ID)
	if err != nil || plan.Outcome(ev.Outcome) == plan.Optimise {
		t.Errorf("an optimised film is still planned: %+v, %v", ev, err)
	}
	st, _ := d.st.Settings(ctx)
	if !st.DryRun {
		t.Error("Dry Run is off after a normal start")
	}
	checkInside(t, dir)
}

func TestDemoReuseAndRunning(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "demo")
	first := buildDemo(t, dir, false)
	first.close()

	d := buildDemo(t, dir, true)
	if d.fresh {
		t.Fatal("the second start rebuilt the database instead of reusing it")
	}
	srv := httptest.NewServer(d.handler())
	defer srv.Close()
	if body := get(t, srv.URL, "/queue"); !strings.Contains(body, "43%") {
		t.Error("the queue does not show the running job's progress")
	}
	st, _ := d.st.Settings(context.Background())
	if st.DryRun {
		t.Error("Dry Run is on with -running")
	}
	d.close()

	again := buildDemo(t, dir, false)
	jobs, err := again.st.ActiveJobs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range jobs {
		if j.Status != store.JobWaiting {
			t.Errorf("%s is %s after a start without -running", j.ItemName, j.Status)
		}
	}
}

func TestDemoRefusesAFolderItDidNotMake(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := openFolder(dir, true); err == nil {
		t.Fatal("the demo accepted a folder with someone else's files")
	}
	if _, err := os.Stat(filepath.Join(dir, "keep.txt")); err != nil {
		t.Fatal("the demo deleted a file it did not make")
	}
	d := demoFolder{root: filepath.Join(dir, "demo")}
	if err := d.sparseFile(filepath.Join(dir, "outside.mkv"), 10, demoNow()); err == nil {
		t.Error("the demo wrote a file outside its folder")
	}
}

// checkInside makes sure the demo wrote nothing next to its folder, and
// that the seeding copy of the hard-linked film is inside it.
func checkInside(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(dir))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(dir) {
		t.Errorf("the demo wrote outside %s: %v", dir, entries)
	}
	link := filepath.Join(dir, "media", "downloads", "Nine Bells for Hollin (2018).mkv")
	if _, err := os.Stat(link); err != nil {
		t.Errorf("no seeding copy: %v", err)
	}
}
