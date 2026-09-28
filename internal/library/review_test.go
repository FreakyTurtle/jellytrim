package library

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/freakyturtle/jellytrim/internal/jellyfin/jellyfintest"
	"github.com/freakyturtle/jellytrim/internal/plan"
	"github.com/freakyturtle/jellytrim/internal/policy"
	"github.com/freakyturtle/jellytrim/internal/store"
)

// IDs from the captured fixtures.
const (
	mikeSeriesID     = "f724a7683a50e39821f7e7ecdda5dc95"
	novemberSeasonID = "5942a4165ed6de5d1dae8112a8466322"
	boxSetID         = "b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0"
)

func TestBoxSetHoldingSeriesAndSeasonProtectsEpisodes(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	// Jellyfin lists a series or season as the box set's member, not its
	// episodes.
	e.jf.AddItem(jellyfintest.Item{ID: mikeSeriesID, Name: "Mike Show", Type: "Series", IsFolder: true, Path: "/media/tv/Mike Show"})
	e.jf.AddItem(jellyfintest.Item{ID: novemberSeasonID, Name: "Season 1", Type: "Season", IsFolder: true,
		Path: "/media/tv/November Show/Season 01"})
	e.jf.AddCollection(jellyfintest.Collection{ID: boxSetID, Name: "Keepers", ItemIDs: []string{mikeSeriesID, novemberSeasonID}})
	enableAll(t, e)
	_, err := e.svc.SavePolicy(ctx, policy.Policy{Name: "Keep the box set", Enabled: true, Priority: 1,
		Scope: policy.Scope{Collections: []string{boxSetID}}, Action: policy.Action{Kind: policy.KindProtect}})
	must(t, err)
	_, err = e.svc.Run(ctx)
	must(t, err)
	for _, name := range []string{"Mike Show - S01E01", "Mike Show - S01E03", "November Show - S01E02"} {
		ev := outcomeOf(t, e, name)
		if plan.Outcome(ev.Outcome) != plan.Protected || !strings.Contains(ev.Summary, "Keep the box set") {
			t.Errorf("%s: %s (%s), want protected by the box set", name, ev.Outcome, ev.Summary)
		}
	}
	if ev := outcomeOf(t, e, "Alpha"); plan.Outcome(ev.Outcome) == plan.Protected {
		t.Errorf("a movie outside the box set was protected: %s", ev.Summary)
	}
}

func TestItemsAreListedWithoutAUser(t *testing.T) {
	e := setup(t)
	_, err := e.svc.Run(context.Background())
	must(t, err)
	listed := 0
	for _, r := range e.jf.RequestsFor("/Items") {
		if r.Query.Get("includeItemTypes") != "Movie,Episode" || r.Query.Get("fields") == "" {
			continue // user data and collection passes
		}
		listed++
		if r.Query.Get("userId") != "" {
			t.Errorf("item listing depends on a user: %v", r.Query)
		}
	}
	if listed == 0 {
		t.Fatal("no item listing requests seen")
	}
}

func TestSyncFailuresAreRecorded(t *testing.T) {
	cases := []struct {
		name    string
		breakIt func(t *testing.T, e env)
		want    string
	}{
		{"Jellyfin unreachable", func(t *testing.T, e env) {
			must(t, e.st.SetSetting(context.Background(), store.KeyJellyfinURL, "http://127.0.0.1:1"))
		}, "reading libraries"},
		{"no managed libraries", func(t *testing.T, e env) {
			must(t, e.st.SetManagedLibraries(context.Background(), nil))
		}, "no libraries are selected"},
		{"reading users fails", func(_ *testing.T, e env) {
			for range 3 { // every retry
				e.jf.FailNext("/Users", 500, 0)
			}
		}, "reading users"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := setup(t)
			tc.breakIt(t, e)
			if _, err := e.svc.Run(context.Background()); err == nil {
				t.Fatal("expected the sync to fail")
			}
			last, err := e.st.LastSync(context.Background())
			must(t, err)
			if last.Status != "failed" || last.FinishedAt == nil || !strings.Contains(last.Error, tc.want) {
				t.Fatalf("sync run %+v, want a failed run mentioning %q", last, tc.want)
			}
		})
	}
}

func TestEmptyLibraryListingSweepsNothing(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	_, err := e.svc.Run(ctx)
	must(t, err)
	items, _ := e.st.ManagedItems(ctx)
	for _, it := range items {
		if it.LibraryID == jellyfintest.FixtureMoviesLibraryID {
			e.jf.RemoveItem(it.ID)
		}
	}
	_, err = e.svc.Run(ctx)
	if err == nil || !strings.Contains(err.Error(), "returned no items") {
		t.Fatalf("an empty library was accepted: %v", err)
	}
	if after, _ := e.st.ManagedItems(ctx); len(after) != len(items) {
		t.Fatalf("items swept after an empty listing: %d of %d left", len(after), len(items))
	}
}

func TestExclusionsApplyToPreviewAndReassess(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	_, err := e.svc.Run(ctx)
	must(t, err)
	efficient := policyNamed(t, e, "Efficient encoding")
	before, err := e.svc.Preview(ctx, efficient)
	must(t, err)
	alpha := mustItemID(t, e, "Alpha")
	must(t, e.st.ExcludeItem(ctx, alpha, "You restored the original."))
	after, err := e.svc.Preview(ctx, efficient)
	must(t, err)
	if after.Matches != before.Matches-1 || after.Optimise != before.Optimise-1 {
		t.Fatalf("preview counted an excluded item: before %+v, after %+v", before, after)
	}
	for _, s := range after.Samples {
		if s.ItemID == alpha {
			t.Fatal("excluded item in preview samples")
		}
	}

	calls := e.prober.calls.Load()
	a, err := e.svc.Reassess(ctx, alpha)
	must(t, err)
	if a.Decision.Outcome != plan.Protected || a.Decision.Summary != "You restored the original." {
		t.Fatalf("reassess of an excluded item: %+v", a.Decision)
	}
	if e.prober.calls.Load() != calls {
		t.Fatal("an excluded item was probed")
	}
}

func TestReassessUsesItsOwnProbe(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	enableAll(t, e)
	_, err := e.svc.Run(ctx)
	must(t, err)
	a, err := e.svc.Reassess(ctx, mustItemID(t, e, "Alpha"))
	must(t, err)
	if a.Decision.Outcome != plan.Optimise || a.File == nil || a.Candidate.File != a.File || len(a.Roots) != 2 {
		t.Fatalf("assessment %+v", a)
	}
	p, err := e.st.Probe(ctx, a.Candidate.Item.ID)
	must(t, err)
	if p.Dev != a.Identity.Dev || p.Inode != a.Identity.Inode || p.Size != a.Identity.Size || p.MtimeNs != a.Identity.MtimeNs {
		t.Fatalf("stored probe %+v does not match the assessed file %+v", p.FileIdentity, a.Identity)
	}
}

// changingProber rewrites the file on every probe, as a download client
// still writing it would.
type changingProber struct{ inner *fakeProber }

func (c changingProber) Probe(ctx context.Context, path string) ([]byte, []byte, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0) // #nosec G304 -- a test file in t.TempDir
	if err != nil {
		return nil, nil, err
	}
	_, _ = f.Write([]byte("more"))
	_ = f.Close()
	return c.inner.Probe(ctx, path)
}

func TestReassessGivesUpOnAChangingFile(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	enableAll(t, e)
	_, err := e.svc.Run(ctx)
	must(t, err)
	svc := New(Options{Store: e.st, Prober: changingProber{inner: e.prober}, Encoders: allEncoders{}, Now: e.svc.now})
	if _, err := svc.Reassess(ctx, mustItemID(t, e, "Alpha")); !errors.Is(err, ErrFileChanging) {
		t.Fatalf("err %v, want ErrFileChanging", err)
	}
}

func TestProbeOfAnotherPathIsNotUsed(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	enableAll(t, e)
	_, err := e.svc.Run(ctx)
	must(t, err)
	alpha := mustItemID(t, e, "Alpha")
	p, err := e.st.Probe(ctx, alpha)
	must(t, err)
	p.LocalPath = "/somewhere/else.mkv"
	must(t, e.st.SaveProbe(ctx, p))
	_, err = e.svc.Evaluate(ctx)
	must(t, err)
	if ev, err := e.st.Evaluation(ctx, alpha); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a probe of another path was evaluated: %+v", ev)
	}
}

func TestOptimisedCheckFailureSkipsTheItem(t *testing.T) {
	probe, frames, err := (&fakeProber{}).Probe(context.Background(), "/x/Alpha.mkv")
	must(t, err)
	it := store.Item{ID: "a", LocalPath: "/x/Alpha.mkv"}
	p := &store.Probe{ItemID: "a", LocalPath: it.LocalPath, ProbeJSON: string(probe), FrameJSON: string(frames)}
	var ec evalContext
	c := ec.candidate(it, p, nil, func(store.FileIdentity) (bool, error) { return false, errors.New("disk I/O error") })
	d, g := gate(c, nil)
	if g != gateDecided || d.Outcome != plan.Skipped || d.Reasons[0].Code != "check_failed" {
		t.Fatalf("gate %v %+v", g, d)
	}
}

func TestNoBackgroundRunAfterShutdown(t *testing.T) {
	e := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	e.svc.Start(ctx)
	cancel()
	if e.svc.RunAsync() || e.svc.EvaluateAsync() || e.svc.RunProbeAsync() {
		t.Fatal("a background run started after the context ended")
	}
	e2 := setup(t)
	e2.svc.Start(context.Background())
	e2.svc.Wait()
	if e2.svc.EvaluateAsync() {
		t.Fatal("a background run started after Wait")
	}
}

func TestReassessOutsideManagedLibrary(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	_, err := e.svc.Run(ctx)
	must(t, err)
	alpha := mustItemID(t, e, "Alpha")
	must(t, e.st.SetManagedLibraries(ctx, []string{jellyfintest.FixtureTVLibraryID}))
	if _, err := e.svc.Reassess(ctx, alpha); err == nil || !strings.Contains(err.Error(), "managed library") {
		t.Fatalf("err %v", err)
	}
}

func policyNamed(t *testing.T, e env, name string) policy.Policy {
	t.Helper()
	ps, err := e.svc.Policies(context.Background())
	must(t, err)
	for _, p := range ps {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("no policy %q", name)
	return policy.Policy{}
}
