package store

import (
	"context"
	"testing"
	"time"
)

func intp(n int) *int { return &n }

func seed(t *testing.T, s *Store) int64 {
	t.Helper()
	ctx := context.Background()
	if err := s.ReplaceLibraries(ctx, []Library{
		{ID: "lm", Name: "Movies", CollectionType: "movies", Locations: []string{"/media/movies"}},
		{ID: "lt", Name: "TV", CollectionType: "tvshows", Locations: []string{"/media/tv"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetManagedLibraries(ctx, []string{"lm", "lt"}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceUsers(ctx, []JellyfinUser{{ID: "u1", Name: "alex"}, {ID: "u2", Name: "sam", Disabled: true}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSelectedUsers(ctx, []string{"u1", "u2"}); err != nil {
		t.Fatal(err)
	}
	id, err := s.StartSync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	added := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.UpsertItems(ctx, id, []Item{
		{ID: "a", LibraryID: "lm", Type: "Movie", Name: "Alpha", SortName: "alpha", JellyfinPath: "/media/movies/a.mkv", LocalPath: "/mnt/movies/a.mkv", DateAdded: &added, Tags: []string{"keep"}},
		{ID: "b", LibraryID: "lm", Type: "Movie", Name: "Bravo_100%", SortName: "bravo", JellyfinPath: "/media/movies/b.mkv"},
		{ID: "e1", LibraryID: "lt", Type: "Episode", Name: "Pilot", SortName: "pilot", SeriesID: "s1", SeriesName: "Show", SeasonNumber: intp(1), EpisodeNumber: intp(1), JellyfinPath: "/media/tv/e1.mkv"},
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestSyncMarkAndSweep(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	first := seed(t, s)
	if _, err := s.FinishSync(ctx, first, true, 3, ""); err != nil {
		t.Fatal(err)
	}

	// A second sync sees only "a". A failed sync must remove nothing.
	second, _ := s.StartSync(ctx)
	if err := s.UpsertItems(ctx, second, []Item{{ID: "a", LibraryID: "lm", Type: "Movie", Name: "Alpha", JellyfinPath: "/media/movies/a.mkv"}}); err != nil {
		t.Fatal(err)
	}
	if removed, err := s.FinishSync(ctx, second, false, 1, "boom"); err != nil || removed != 0 {
		t.Fatalf("failed sync removed %d items (%v)", removed, err)
	}
	if items, _ := s.ManagedItems(ctx); len(items) != 3 {
		t.Fatalf("after failed sync: %d items", len(items))
	}

	third, _ := s.StartSync(ctx)
	if err := s.UpsertItems(ctx, third, []Item{{ID: "a", LibraryID: "lm", Type: "Movie", Name: "Alpha", JellyfinPath: "/media/movies/a.mkv"}}); err != nil {
		t.Fatal(err)
	}
	removed, err := s.FinishSync(ctx, third, true, 1, "")
	if err != nil || removed != 2 {
		t.Fatalf("complete sync removed %d (%v), want 2", removed, err)
	}
	last, err := s.LastSync(ctx)
	if err != nil || last.Status != "complete" || last.ItemsSeen != 1 {
		t.Fatalf("last sync %+v %v", last, err)
	}
}

func TestLibrariesKeepManagedFlag(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	seed(t, s)
	if err := s.ReplaceLibraries(ctx, []Library{{ID: "lm", Name: "Films", Locations: []string{"/media/movies"}}}); err != nil {
		t.Fatal(err)
	}
	libs, _ := s.Libraries(ctx)
	if len(libs) != 1 || !libs[0].Managed || libs[0].Name != "Films" || libs[0].Locations[0] != "/media/movies" {
		t.Fatalf("libraries %+v", libs)
	}
}

func TestUserDataAndSelection(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	seed(t, s)
	played := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if err := s.ReplaceUserData(ctx, "u1", []UserData{{ItemID: "a", Played: true, PlayCount: 2, LastPlayedAt: &played}, {ItemID: "missing", Played: true}}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceUserData(ctx, "u2", []UserData{{ItemID: "a", Favourite: true}}); err != nil {
		t.Fatal(err)
	}
	ud, err := s.AllUserData(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// u2 is disabled, so only u1 counts; the missing item was ignored.
	if len(ud) != 1 || len(ud["a"]) != 1 || !ud["a"][0].Played || ud["a"][0].LastPlayedAt.Unix() != played.Unix() {
		t.Fatalf("user data %+v", ud)
	}
	sel, _ := s.SelectedUsers(ctx)
	if len(sel) != 1 || sel[0].ID != "u1" {
		t.Fatalf("selected %+v", sel)
	}
}

func TestLibraryListFiltersAndSearch(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	seed(t, s)
	if err := s.SaveProbe(ctx, Probe{ItemID: "a", LocalPath: "/mnt/movies/a.mkv", FileIdentity: FileIdentity{Size: 5e9}, Nlink: 1,
		VideoCodec: "h264", Width: 3840, Height: 2160, Resolution: 2160, HDR: "hdr10"}); err != nil {
		t.Fatal(err)
	}
	lo, hi := int64(1e9), int64(2e9)
	pid := int64(1)
	if err := s.ReplaceEvaluations(ctx, []Evaluation{{ItemID: "a", Outcome: "optimise", PolicyID: &pid, Summary: "x", EstMin: &lo, EstMax: &hi},
		{ItemID: "b", Outcome: "skipped", Summary: "y"}}); err != nil {
		t.Fatal(err)
	}
	check := func(f LibraryFilter, want ...string) {
		t.Helper()
		rows, total, err := s.LibraryList(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, r := range rows {
			got = append(got, r.Item.ID)
		}
		if total != len(want) || len(got) != len(want) {
			t.Fatalf("filter %+v: got %v (total %d), want %v", f, got, total, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("filter %+v: got %v, want %v", f, got, want)
			}
		}
	}
	check(LibraryFilter{}, "a", "b", "e1")
	check(LibraryFilter{Outcome: "optimise"}, "a")
	check(LibraryFilter{Outcome: "pending"}, "e1")
	check(LibraryFilter{Resolution: 2160, Codec: "h264", HDR: true}, "a")
	check(LibraryFilter{Search: "100%"}, "b")
	check(LibraryFilter{Search: "sho"}, "e1")
	check(LibraryFilter{Search: "_"}, "b")

	tot, err := s.Totals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if tot.Items != 3 || tot.ByOutcome["optimise"] != 1 || tot.EstMin != lo || tot.EstMax != hi || tot.Unprobed != 2 {
		t.Fatalf("totals %+v", tot)
	}
}

func TestPoliciesOrderAndMove(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	var ids []int64
	for _, n := range []string{"one", "two", "three"} {
		id, err := s.SavePolicy(ctx, PolicyRow{Name: n, Scope: "{}", Conditions: "{}", Action: "{}"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := s.MovePolicy(ctx, ids[2], true); err != nil {
		t.Fatal(err)
	}
	ps, _ := s.Policies(ctx)
	if ps[0].Name != "one" || ps[1].Name != "three" || ps[2].Name != "two" {
		t.Fatalf("order %s %s %s", ps[0].Name, ps[1].Name, ps[2].Name)
	}
	if err := s.MovePolicy(ctx, ids[0], true); err != nil { // already first: no change
		t.Fatal(err)
	}
	if _, err := s.SavePolicy(ctx, PolicyRow{ID: 999, Name: "x"}); err != ErrNotFound {
		t.Fatalf("updating a missing policy: %v", err)
	}
}

func TestOptimisedIdentity(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	id := FileIdentity{Dev: 1, Inode: 2, Size: 3, MtimeNs: 4}
	if ok, _ := s.IsOptimised(ctx, id); ok {
		t.Fatal("unexpected")
	}
	if err := s.RecordOptimised(ctx, id, "a", 1); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.IsOptimised(ctx, id); !ok {
		t.Fatal("not recorded")
	}
	id.MtimeNs++
	if ok, _ := s.IsOptimised(ctx, id); ok {
		t.Fatal("a changed file must not count as optimised")
	}
}
