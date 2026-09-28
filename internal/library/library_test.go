package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/freakyturtle/jellytrim/internal/jellyfin/jellyfintest"
	"github.com/freakyturtle/jellytrim/internal/media"
	"github.com/freakyturtle/jellytrim/internal/plan"
	"github.com/freakyturtle/jellytrim/internal/policy"
	"github.com/freakyturtle/jellytrim/internal/store"
)

// fixtureFor maps the dev-stack file names (as captured from Jellyfin) to
// the probe fixtures made from the same clips.
var fixtureFor = map[string]string{
	"Alpha": "h264-1080p", "Bravo": "hevc-1080p", "Charlie": "h264-2160p", "Delta": "hevc-2160p-hdr10",
	"Echo": "hevc-1080p-hlg", "Foxtrot": "av1-1080p", "Golf": "multi-audio-subs", "Hotel": "anime-ass",
	"India": "scope-1080p", "Juliet": "interlaced", "Kilo": "mp4-movtext-cover", "Lima": "h264-1080p",
	"Mike Show": "tv-episode", "November Show": "hevc-1080p",
}

type fakeProber struct{ calls atomic.Int32 }

func (p *fakeProber) Probe(_ context.Context, path string) ([]byte, []byte, error) {
	p.calls.Add(1)
	base := filepath.Base(path)
	for prefix, fx := range fixtureFor {
		if strings.HasPrefix(base, prefix) {
			dir := filepath.Join("..", "media", "testdata", "probe")
			probe, err := os.ReadFile(filepath.Join(dir, fx+".json"))
			if err != nil {
				return nil, nil, err
			}
			frames, _ := os.ReadFile(filepath.Join(dir, fx+".frames.json"))
			return probe, frames, nil
		}
	}
	return nil, nil, errors.New("Invalid data found when processing input")
}

type allEncoders struct{}

func (allEncoders) Select(media.Codec, bool, string) (string, bool, string) { return "x265", true, "" }

type env struct {
	svc    *Service
	st     *store.Store
	jf     *jellyfintest.Server
	prober *fakeProber
	root   string
}

func setup(t *testing.T) env {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	jf := jellyfintest.New(t, jellyfintest.WithFixtures())
	root := t.TempDir()
	// Recreate the fixture library's files (content does not matter: the
	// fake prober answers from the probe fixtures).
	for _, rel := range []string{
		"movies/Alpha (2019)/Alpha (2019).mkv", "movies/Bravo (2020)/Bravo (2020).mkv", "movies/Charlie (2021)/Charlie (2021).mkv",
		"movies/Delta (2022)/Delta (2022).mkv", "movies/Echo (2022)/Echo (2022).mkv", "movies/Foxtrot (2023)/Foxtrot (2023).mkv",
		"movies/Golf (2018)/Golf (2018).mkv", "movies/Hotel (2017)/Hotel (2017).mkv", "movies/India (2016)/India (2016).mkv",
		"movies/Juliet (2005)/Juliet (2005).mkv", "movies/Kilo (2015)/Kilo (2015).mp4", "movies/Lima (2014)/Lima (2014).mkv",
		"tv/Mike Show/Season 01/Mike Show - S01E01.mkv", "tv/Mike Show/Season 01/Mike Show - S01E02.mkv",
		"tv/Mike Show/Season 01/Mike Show - S01E03.mkv", "tv/November Show/Season 01/November Show - S01E01.mkv",
		"tv/November Show/Season 01/November Show - S01E02.mkv",
	} {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(rel), 0o644); err != nil {
			t.Fatal(err)
		}
		// Sparse-extend to the real clip's size so estimates behave.
		if size := fixtureSize(t, filepath.Base(p)); size > 0 {
			must(t, os.Truncate(p, size))
		}
	}
	prober := &fakeProber{}
	svc := New(Options{Store: st, Prober: prober, Encoders: allEncoders{},
		Now: func() time.Time { return time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC) }})
	if _, err := svc.SaveConnection(ctx, jf.URL, jf.APIKey()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := svc.RefreshServerInfo(ctx); err != nil {
		t.Fatal(err)
	}
	libs, _ := st.Libraries(ctx)
	var ids []string
	for _, l := range libs {
		ids = append(ids, l.ID)
	}
	must(t, st.SetManagedLibraries(ctx, ids))
	must(t, st.SetSelectedUsers(ctx, []string{jellyfintest.FixtureUserID}))
	must(t, st.ReplacePathMappings(ctx, []store.PathMapping{
		{JellyfinPrefix: "/media/movies", LocalPrefix: filepath.ToSlash(filepath.Join(root, "movies"))},
		{JellyfinPrefix: "/media/tv", LocalPrefix: filepath.ToSlash(filepath.Join(root, "tv"))},
	}))
	must(t, svc.CreateStarterPolicies(ctx, nil))
	return env{svc: svc, st: st, jf: jf, prober: prober, root: root}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func enableAll(t *testing.T, e env) {
	t.Helper()
	ps, err := e.svc.Policies(context.Background())
	must(t, err)
	for _, p := range ps {
		must(t, e.st.SetPolicyEnabled(context.Background(), p.ID, true))
	}
}

func outcomeOf(t *testing.T, e env, name string) store.Evaluation {
	t.Helper()
	ctx := context.Background()
	rows, _, err := e.st.LibraryList(ctx, store.LibraryFilter{Search: name, Limit: 5})
	must(t, err)
	if len(rows) == 0 {
		t.Fatalf("item %q not found", name)
	}
	ev, err := e.st.Evaluation(ctx, rows[0].Item.ID)
	must(t, err)
	return ev
}

func TestRunSyncsProbesAndEvaluates(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	enableAll(t, e)
	res, err := e.svc.Run(ctx)
	must(t, err)
	if res.Items != 17 || res.Probed != 17 || res.Failures != 0 {
		t.Fatalf("result %+v", res)
	}
	want := map[string]plan.Outcome{
		"Alpha":   plan.Optimise,  // H.264 at 8 Mbps to HEVC
		"Charlie": plan.Optimise,  // watched long ago, 4K: archived to 1080p
		"Juliet":  plan.Skipped,   // interlaced
		"Foxtrot": plan.NoPolicy,  // AV1: no starter policy applies
		"Bravo":   plan.Protected, // a favourite in the fixture data
		"Echo":    plan.NoPolicy,  // HLG HEVC 1080p, unwatched
		"Golf":    plan.Protected, // favourite
		"Lima":    plan.Optimise,  // fake files are not hard-linked here
		"Kilo":    plan.Optimise,  // MP4 with mov_text and cover art
		"Hotel":   plan.Optimise,  // ASS and fonts in MKV
		"Mike":    plan.Optimise,  // TV episode 1080p H.264
	}
	for name, outcome := range want {
		if got := outcomeOf(t, e, name); plan.Outcome(got.Outcome) != outcome {
			t.Errorf("%s: %s (%s), want %s", name, got.Outcome, got.Summary, outcome)
		}
	}

	// Nothing changed on disk: a second run probes nothing.
	calls := e.prober.calls.Load()
	res, err = e.svc.Run(ctx)
	must(t, err)
	if res.Probed != 0 || e.prober.calls.Load() != calls {
		t.Fatalf("unchanged files were probed again: %+v", res)
	}

	// A changed file is probed again.
	p := filepath.Join(e.root, "movies", "Alpha (2019)", "Alpha (2019).mkv")
	must(t, os.WriteFile(p, []byte("a different size"), 0o644))
	res, err = e.svc.Run(ctx)
	must(t, err)
	if res.Probed != 1 {
		t.Fatalf("changed file: probed %d", res.Probed)
	}
}

func TestMissingFileAndUnmappedPath(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	must(t, os.Remove(filepath.Join(e.root, "movies", "Bravo (2020)", "Bravo (2020).mkv")))
	must(t, e.st.ReplacePathMappings(ctx, []store.PathMapping{
		{JellyfinPrefix: "/media/movies", LocalPrefix: filepath.ToSlash(filepath.Join(e.root, "movies"))},
	}))
	res, err := e.svc.Run(ctx)
	must(t, err)
	if res.Failures != 1 {
		t.Fatalf("failures %d", res.Failures)
	}
	p, err := e.st.Probe(ctx, mustItemID(t, e, "Bravo"))
	must(t, err)
	if !strings.Contains(p.Error, "was not found") {
		t.Fatalf("probe error %q", p.Error)
	}
	tv := outcomeOf(t, e, "Mike")
	if tv.Outcome != string(plan.Skipped) || !strings.Contains(tv.Summary, "No path mapping covers") {
		t.Fatalf("unmapped: %+v", tv)
	}
}

func mustItemID(t *testing.T, e env, name string) string {
	rows, _, err := e.st.LibraryList(context.Background(), store.LibraryFilter{Search: name, Limit: 1})
	must(t, err)
	return rows[0].Item.ID
}

func TestFailedSyncRemovesNothing(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	_, err := e.svc.Run(ctx)
	must(t, err)
	e.jf.FailNext("/Items", 500, 0)
	e.jf.FailNext("/Items", 500, 0)
	e.jf.FailNext("/Items", 500, 0)
	if _, err := e.svc.Run(ctx); err == nil {
		t.Fatal("expected the sync to fail")
	}
	items, _ := e.st.ManagedItems(ctx)
	if len(items) != 17 {
		t.Fatalf("a failed sync removed items: %d left", len(items))
	}
}

func TestPreviewAndDryRun(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	_, err := e.svc.Run(ctx) // only Protect favourites is enabled
	must(t, err)
	ps, _ := e.svc.Policies(ctx)
	var efficient policy.Policy
	for _, p := range ps {
		if p.Name == "Efficient encoding" {
			efficient = p
		}
	}
	pv, err := e.svc.Preview(ctx, efficient)
	must(t, err)
	if pv.Matches == 0 || pv.Optimise == 0 || pv.Wins > pv.Matches || pv.AfterMax > pv.Current {
		t.Fatalf("preview %+v", pv)
	}

	// Enable it and evaluate: the Dry Run must agree with the preview.
	must(t, e.st.SetPolicyEnabled(ctx, efficient.ID, true))
	_, err = e.svc.Evaluate(ctx)
	must(t, err)
	sum, err := e.svc.DryRun(ctx)
	must(t, err)
	if sum.Optimise != pv.Optimise {
		t.Fatalf("dry run optimise %d, preview %d", sum.Optimise, pv.Optimise)
	}
	if sum.SavingMin() <= 0 || sum.SavingMax() < sum.SavingMin() || sum.Evaluated != 17 {
		t.Fatalf("summary %+v", sum)
	}
	found := false
	for _, l := range sum.Lines {
		if l.Kind == "convert" && strings.Contains(l.Text, "H.264 → HEVC") {
			found = true
		}
	}
	if !found {
		t.Fatalf("lines %+v", sum.Lines)
	}
}

func TestBusy(t *testing.T) {
	e := setup(t)
	e.svc.syncMu.Lock()
	defer e.svc.syncMu.Unlock()
	if _, err := e.svc.Run(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatalf("err %v", err)
	}
}

func TestSuggestMappings(t *testing.T) {
	libs := []store.Library{{Locations: []string{"/media/movies", `D:\Media\TV`}}}
	exists := func(p string) bool { return p == "/mnt/media/movies" || p == "/data/TV" }
	got := SuggestMappings(libs, exists)
	if len(got) != 2 || got[0].LocalPrefix != "/mnt/media/movies" || got[1].LocalPrefix != "/data/TV" {
		t.Fatalf("suggestions %+v", got)
	}
}

func TestWriteTest(t *testing.T) {
	dir := t.TempDir()
	w, hl, problem := writeTest(dir)
	if !w || !hl || problem != "" {
		t.Fatalf("writeTest: %v %v %q", w, hl, problem)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("write test left files behind: %v", entries)
	}
}

func fixtureSize(t *testing.T, base string) int64 {
	t.Helper()
	for prefix, fx := range fixtureFor {
		if strings.HasPrefix(base, prefix) {
			b, err := os.ReadFile(filepath.Join("..", "media", "testdata", "probe", fx+".json"))
			must(t, err)
			f, err := media.Parse(b, nil, 0)
			must(t, err)
			return f.Size
		}
	}
	return 0
}
