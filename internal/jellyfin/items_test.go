package jellyfin_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"testing"
	"time"

	"github.com/freakyturtle/jellytrim/internal/jellyfin"
	"github.com/freakyturtle/jellytrim/internal/jellyfin/jellyfintest"
)

const alphaID = "d15b890b81018d54a2b0f65fbee75563"

// collect runs AllItems and returns the IDs passed to fn, in order.
func collect(t *testing.T, c *jellyfin.Client, q jellyfin.ItemQuery) ([]string, int, error) {
	t.Helper()
	var ids []string
	n, err := c.AllItems(context.Background(), q, func(it jellyfin.Item) error {
		ids = append(ids, it.ID)
		return nil
	})
	return ids, n, err
}

func assertUnique(t *testing.T, ids []string) {
	t.Helper()
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Errorf("item %s passed to fn twice", id)
		}
		seen[id] = true
	}
}

func TestItemsRequestsTheAgreedQuery(t *testing.T) {
	h := newHarness(t, fixtures())
	page, err := h.c.Items(context.Background(), jellyfin.ItemQuery{
		UserID: jellyfintest.FixtureUserID, ParentID: jellyfintest.FixtureMoviesLibraryID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.TotalRecordCount != 12 || len(page.Items) != 12 {
		t.Fatalf("movies: total %d, items %d", page.TotalRecordCount, len(page.Items))
	}
	q := h.srv.RequestsFor("/Items")[0].Query
	want := map[string]string{
		"userId":           jellyfintest.FixtureUserID,
		"parentId":         jellyfintest.FixtureMoviesLibraryID,
		"recursive":        "true",
		"includeItemTypes": "Movie,Episode",
		"fields":           "Path,MediaSources,DateCreated,Tags,Genres,ProviderIds,ParentId,SortName",
		"enableUserData":   "true",
		"enableImageTypes": "Primary",
		"imageTypeLimit":   "1",
		"startIndex":       "0",
		"limit":            "200",
		"sortBy":           "SortName,Id",
		"sortOrder":        "Ascending",
	}
	for k, v := range want {
		if got := q.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	alpha := page.Items[0]
	if alpha.ID != alphaID || alpha.Path != "/media/movies/Alpha (2019)/Alpha (2019).mkv" {
		t.Fatalf("first movie: %+v", alpha)
	}
	if !alpha.UserData.Played || alpha.UserData.PlayCount != 1 || alpha.UserData.LastPlayedDate == nil {
		t.Errorf("Alpha user data: %+v", alpha.UserData)
	}
	if len(alpha.MediaSources) != 1 || alpha.MediaSources[0].MediaStreams[0].Codec != "h264" {
		t.Errorf("Alpha media sources: %+v", alpha.MediaSources)
	}
	if alpha.DateCreated.IsZero() || alpha.DateCreated.Location() != time.UTC {
		t.Errorf("DateCreated = %v", alpha.DateCreated)
	}
}

func TestAllItemsPagesThroughEverything(t *testing.T) {
	h := newHarness(t, fixtures())
	ids, n, err := collect(t, h.c, jellyfin.ItemQuery{UserID: jellyfintest.FixtureUserID, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if n != 17 || len(ids) != 17 {
		t.Fatalf("got %d items (%d calls), want 17", n, len(ids))
	}
	assertUnique(t, ids)
	reqs := h.srv.RequestsFor("/Items")
	if len(reqs) != 4 {
		t.Fatalf("pages = %d, want 4", len(reqs))
	}
	for i, r := range reqs {
		if got, want := r.Query.Get("startIndex"), []string{"0", "5", "10", "15"}[i]; got != want {
			t.Errorf("page %d startIndex = %s, want %s", i, got, want)
		}
	}
}

func TestAllItemsDedupesWhenAScanInsertsAnItem(t *testing.T) {
	h := newHarness(t, fixtures())
	// Sorts first, so every later item moves down one place after page 1.
	h.srv.ShiftPaging(1, jellyfintest.Item{
		ID: "00aa00aa00aa00aa00aa00aa00aa00aa", Name: "Aardvark (2024)", SortName: "!aardvark",
		Type: "Movie", Path: "/media/movies/Aardvark (2024)/Aardvark (2024).mkv", Container: "mkv",
	})
	ids, n, err := collect(t, h.c, jellyfin.ItemQuery{UserID: jellyfintest.FixtureUserID, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	assertUnique(t, ids)
	if n != 18 || len(ids) != 18 {
		t.Fatalf("got %d distinct items, want 18 (17 plus the new one)", n)
	}
	if !slices.Contains(ids, "00aa00aa00aa00aa00aa00aa00aa00aa") {
		t.Errorf("the inserted item was never delivered")
	}
}

func TestAllItemsRereadsWhenAScanRemovesAnItem(t *testing.T) {
	h := newHarness(t, fixtures())
	first, err := h.c.Items(context.Background(), jellyfin.ItemQuery{UserID: jellyfintest.FixtureUserID, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	// Removing the first item after page 1 pulls item 6 into page 1's range,
	// so a single pass would never see it.
	h.srv.RemoveDuringPaging(2, first.Items[0].ID)
	ids, _, err := collect(t, h.c, jellyfin.ItemQuery{UserID: jellyfintest.FixtureUserID, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	assertUnique(t, ids)
	if len(ids) != 17 {
		t.Fatalf("delivered %d items, want all 17 (the removed one was seen on page 1)", len(ids))
	}
}

func TestAllItemsGivesUpWhenTheListKeepsChanging(t *testing.T) {
	h := newHarness(t, fixtures())
	for i := range 40 {
		id := []byte("0000000000000000000000000000000z")
		id[30] = byte('a' + i%26)
		id[29] = byte('a' + i/26)
		h.srv.ShiftPaging(i+1, jellyfintest.Item{ID: string(id), Name: string(id), SortName: "!" + string(id), Type: "Movie"})
	}
	ids, _, err := collect(t, h.c, jellyfin.ItemQuery{UserID: jellyfintest.FixtureUserID, Limit: 5})
	if !errors.Is(err, jellyfin.ErrIncompletePass) {
		t.Fatalf("want ErrIncompletePass, got %v", err)
	}
	assertUnique(t, ids)
}

func TestAllItemsFailsWhenAPageFails(t *testing.T) {
	h := newHarness(t, fixtures())
	calls := 0
	_, err := h.c.AllItems(context.Background(), jellyfin.ItemQuery{UserID: jellyfintest.FixtureUserID, Limit: 5},
		func(jellyfin.Item) error {
			calls++
			if calls == 1 {
				h.srv.FailNext("/Items", 500, 0)
			}
			return nil
		})
	var se *jellyfin.StatusError
	if !errors.As(err, &se) || se.StatusCode != 500 {
		t.Fatalf("want the page's 500, got %v", err)
	}
	if calls != 5 {
		t.Errorf("fn called %d times, want 5 (the first page only)", calls)
	}
}

func TestAllItemsStopsOnCallbackError(t *testing.T) {
	h := newHarness(t, fixtures())
	stop := errors.New("stop")
	_, err := h.c.AllItems(context.Background(), jellyfin.ItemQuery{Limit: 5}, func(jellyfin.Item) error { return stop })
	if !errors.Is(err, stop) {
		t.Fatalf("want the callback's error, got %v", err)
	}
	if n := len(h.srv.RequestsFor("/Items")); n != 1 {
		t.Errorf("pages = %d, want 1", n)
	}
}

// twoUsers builds a small library where two users watched different things.
func twoUsers(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t, nil)
	h.srv.AddLibrary(jellyfintest.Library{Name: "Films", CollectionType: "movies", ItemID: "lib1", Locations: []string{"/media/movies"}})
	h.srv.AddLibrary(jellyfintest.Library{Name: "TV", CollectionType: "tvshows", ItemID: "lib2", Locations: []string{"/media/tv"}})
	h.srv.AddUser(jellyfintest.User{ID: "u1", Name: "alice", IsAdministrator: true})
	h.srv.AddUser(jellyfintest.User{ID: "u2", Name: "bob"})
	for _, it := range []jellyfintest.Item{
		{ID: "m1", Name: "One", Type: "Movie", Path: "/media/movies/One/One.mkv"},
		{ID: "m2", Name: "Two", Type: "Movie", Path: "/media/movies/Two/Two.mkv"},
		{ID: "m3", Name: "Three", Type: "Movie", Path: "/media/movies/Three/Three.mkv"},
		{ID: "e1", Name: "Show - S01E01", Type: "Episode", Path: "/media/tv/Show/S01/e1.mkv", SeriesID: "s1", SeasonID: "se1", ParentID: "se1"},
	} {
		h.srv.AddItem(it)
	}
	watched := time.Date(2026, 5, 1, 20, 0, 0, 0, time.UTC)
	h.srv.SetUserData("u1", "m1", jellyfintest.UserData{Played: true, PlayCount: 2, LastPlayedDate: &watched})
	h.srv.SetUserData("u2", "m2", jellyfintest.UserData{IsFavorite: true})
	return h
}

func TestUserDataIsPerUser(t *testing.T) {
	h := twoUsers(t)
	got := map[string]map[string]jellyfin.UserData{}
	for _, u := range []string{"u1", "u2"} {
		got[u] = map[string]jellyfin.UserData{}
		err := h.c.UserData(context.Background(), u, "lib1", func(id string, ud jellyfin.UserData) error {
			got[u][id] = ud
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(got["u1"]) != 3 || len(got["u2"]) != 3 {
		t.Fatalf("want 3 movies per user, got %d and %d", len(got["u1"]), len(got["u2"]))
	}
	a := got["u1"]["m1"]
	if !a.Played || a.PlayCount != 2 || a.LastPlayedDate == nil || !a.LastPlayedDate.Equal(time.Date(2026, 5, 1, 20, 0, 0, 0, time.UTC)) {
		t.Errorf("alice m1: %+v", a)
	}
	if b := got["u2"]["m1"]; b.Played || b.LastPlayedDate != nil {
		t.Errorf("bob m1 should be unwatched: %+v", b)
	}
	if !got["u2"]["m2"].IsFavorite || got["u1"]["m2"].IsFavorite {
		t.Errorf("favourites leaked between users")
	}
	q := h.srv.RequestsFor("/Items")[0].Query
	if q.Has("fields") || q.Get("enableImages") != "false" || q.Get("enableUserData") != "true" {
		t.Errorf("user data pass should be light: %v", q)
	}
}

func TestUserDataNeedsAUser(t *testing.T) {
	h := twoUsers(t)
	err := h.c.UserData(context.Background(), "", "lib1", func(string, jellyfin.UserData) error { return nil })
	if err == nil {
		t.Fatal("want an error without a user ID")
	}
}

func TestEpisodesMatchTheirLibraryAndSeries(t *testing.T) {
	h := twoUsers(t)
	for parent, want := range map[string]int{"lib2": 1, "s1": 1, "se1": 1, "lib1": 3} {
		_, n, err := collect(t, h.c, jellyfin.ItemQuery{UserID: "u1", ParentID: parent})
		if err != nil || n != want {
			t.Errorf("parent %s: %d items, err %v; want %d", parent, n, err, want)
		}
	}
}

func TestCollections(t *testing.T) {
	h := twoUsers(t)
	h.srv.AddCollection(jellyfintest.Collection{ID: "bs1", Name: "Trilogy", ItemIDs: []string{"m1", "m3"}})
	cols, err := h.c.Collections(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(cols) != 1 || cols[0].ID != "bs1" || cols[0].Name != "Trilogy" {
		t.Fatalf("collections = %+v", cols)
	}
	q := h.srv.RequestsFor("/Items")[0].Query
	if q.Get("includeItemTypes") != "BoxSet" || q.Get("recursive") != "true" {
		t.Errorf("collections query: %v", q)
	}
	ids, err := h.c.CollectionItemIDs(context.Background(), "u1", "bs1")
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []string{"m1", "m3"}) {
		t.Errorf("collection items = %v", ids)
	}
}

func TestNotifyMediaUpdatedBody(t *testing.T) {
	h := newHarness(t, fixtures())
	paths := []string{"/media/movies/Alpha (2019)/Alpha (2019).mkv", `/media/tv/Odd "quoted" name.mkv`}
	if err := h.c.NotifyMediaUpdated(context.Background(), paths...); err != nil {
		t.Fatal(err)
	}
	bodies := h.srv.MediaUpdateBodies()
	if len(bodies) != 1 {
		t.Fatalf("calls = %d", len(bodies))
	}
	var got map[string][]map[string]string
	if err := json.Unmarshal(bodies[0], &got); err != nil {
		t.Fatal(err)
	}
	want := map[string][]map[string]string{"Updates": {
		{"Path": paths[0], "UpdateType": "Modified"},
		{"Path": paths[1], "UpdateType": "Modified"},
	}}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("body\n got: %s\nwant: %s", gotJSON, wantJSON)
	}
	if ct := h.srv.RequestsFor("/Library/Media/Updated")[0].Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	if err := h.c.NotifyMediaUpdated(context.Background()); err != nil || len(h.srv.MediaUpdateBodies()) != 1 {
		t.Errorf("no paths should send nothing: %v", err)
	}
}

func TestRefreshItem(t *testing.T) {
	h := newHarness(t, fixtures())
	if err := h.c.RefreshItem(context.Background(), alphaID); err != nil {
		t.Fatal(err)
	}
	r := h.srv.Refreshes()
	if len(r) != 1 || r[0].ItemID != alphaID {
		t.Fatalf("refreshes = %+v", r)
	}
	want := map[string]string{
		"metadataRefreshMode": "FullRefresh", "imageRefreshMode": "None",
		"replaceAllMetadata": "false", "replaceAllImages": "false",
	}
	for k, v := range want {
		if r[0].Query.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, r[0].Query.Get(k), v)
		}
	}
	if err := h.c.RefreshItem(context.Background(), "ffffffffffffffffffffffffffffffff"); !errors.Is(err, jellyfin.ErrNotFound) {
		t.Errorf("refreshing a missing item: %v", err)
	}
}

func TestItemUsesTheCurrentRoute(t *testing.T) {
	h := newHarness(t, fixtures())
	it, err := h.c.Item(context.Background(), jellyfintest.FixtureUserID, alphaID)
	if err != nil {
		t.Fatal(err)
	}
	if it.Path != "/media/movies/Alpha (2019)/Alpha (2019).mkv" || len(it.MediaSources) != 1 || !it.UserData.Played {
		t.Errorf("item: %+v", it)
	}
	reqs := h.srv.Requests()
	if len(reqs) != 1 || reqs[0].Path != "/Items/"+alphaID || reqs[0].Query.Get("userId") != jellyfintest.FixtureUserID {
		t.Errorf("requests: %+v", reqs)
	}
}

func TestItemFallsBackToTheUserRoute(t *testing.T) {
	h := newHarness(t, []jellyfintest.Option{jellyfintest.WithFixtures(), jellyfintest.WithLegacyItemRoute()})
	it, err := h.c.Item(context.Background(), jellyfintest.FixtureUserID, alphaID)
	if err != nil {
		t.Fatal(err)
	}
	if it.ID != alphaID {
		t.Errorf("item: %+v", it)
	}
	var paths []string
	for _, r := range h.srv.Requests() {
		paths = append(paths, r.Path)
	}
	want := []string{"/Items/" + alphaID, "/Users/" + jellyfintest.FixtureUserID + "/Items/" + alphaID}
	if !slices.Equal(paths, want) {
		t.Errorf("paths = %v, want %v", paths, want)
	}
}

func TestItemNotFound(t *testing.T) {
	h := newHarness(t, fixtures())
	for _, id := range []string{"ffffffffffffffffffffffffffffffff", "00000000000000000000000000000000"} {
		if _, err := h.c.Item(context.Background(), jellyfintest.FixtureUserID, id); !errors.Is(err, jellyfin.ErrNotFound) {
			t.Errorf("Item(%s): want ErrNotFound, got %v", id, err)
		}
	}
}

func TestIDsArePathEscaped(t *testing.T) {
	h := newHarness(t, fixtures())
	_, _ = h.c.Item(context.Background(), "", "a/../../System/Info")
	reqs := h.srv.Requests()
	if len(reqs) == 0 || reqs[0].Path != "/Items/a/../../System/Info" {
		t.Fatalf("requests: %+v", reqs)
	}
	for _, r := range reqs {
		if r.Path == "/System/Info" {
			t.Errorf("an ID escaped its path segment")
		}
	}
}

func TestImage(t *testing.T) {
	h := twoUsers(t)
	png := []byte("\x89PNG\r\n\x1a\n0000fake image")
	h.srv.AddItem(jellyfintest.Item{ID: "m9", Name: "Nine", Type: "Movie", Path: "/media/movies/Nine.mkv", Image: png})
	body, ct, err := h.c.Image(context.Background(), "m9", 300)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = body.Close() }()
	b, _ := io.ReadAll(body)
	if string(b) != string(png) || ct != "image/png" {
		t.Errorf("image: %q %q", b, ct)
	}
	q := h.srv.RequestsFor("/Items/m9/Images/Primary")[0].Query
	if q.Get("maxWidth") != "300" || q.Get("quality") != "85" {
		t.Errorf("image query: %v", q)
	}
	if _, _, err := h.c.Image(context.Background(), "m1", 300); !errors.Is(err, jellyfin.ErrNotFound) {
		t.Errorf("no artwork: want ErrNotFound, got %v", err)
	}
	page, err := h.c.Items(context.Background(), jellyfin.ItemQuery{UserID: "u1", ParentID: "lib1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range page.Items {
		if it.HasPrimaryImage() != (it.ID == "m9") {
			t.Errorf("%s HasPrimaryImage = %v", it.ID, it.HasPrimaryImage())
		}
	}
}

func TestSystemLibrariesAndUsersFromFixtures(t *testing.T) {
	h := newHarness(t, fixtures())
	ctx := context.Background()
	pub, err := h.c.PublicInfo(ctx)
	if err != nil || pub.ServerName != "jellytrim-dev" || pub.Version != "12.1.0" || !pub.StartupWizardCompleted {
		t.Errorf("public info: %+v %v", pub, err)
	}
	libs, err := h.c.Libraries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{libs[0].Name, libs[1].Name}
	slices.Sort(names)
	if !slices.Equal(names, []string{"Movies", "TV"}) {
		t.Errorf("libraries: %+v", libs)
	}
	users, err := h.c.Users(ctx)
	if err != nil || len(users) != 1 || users[0].ID != jellyfintest.FixtureUserID || !users[0].Policy.IsAdministrator {
		t.Errorf("users: %+v %v", users, err)
	}
}
