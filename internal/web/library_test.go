package web

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/freakyturtle/jellytrim/internal/jellyfin/jellyfintest"
	"github.com/freakyturtle/jellytrim/internal/library"
	"github.com/freakyturtle/jellytrim/internal/media"
	"github.com/freakyturtle/jellytrim/internal/store"
)

// libraryTestFixtures maps the dev-stack file names to the probe fixtures
// made from the same clips (as in internal/library's tests).
var libraryTestFixtures = map[string]string{
	"Alpha": "h264-1080p", "Bravo": "hevc-1080p", "Charlie": "h264-2160p", "Delta": "hevc-2160p-hdr10",
	"Echo": "hevc-1080p-hlg", "Foxtrot": "av1-1080p", "Golf": "multi-audio-subs", "Hotel": "anime-ass",
	"India": "scope-1080p", "Juliet": "interlaced", "Kilo": "mp4-movtext-cover", "Lima": "h264-1080p",
	"Mike Show": "tv-episode", "November Show": "hevc-1080p",
}

var libraryTestFiles = []string{
	"movies/Alpha (2019)/Alpha (2019).mkv", "movies/Bravo (2020)/Bravo (2020).mkv", "movies/Charlie (2021)/Charlie (2021).mkv",
	"movies/Delta (2022)/Delta (2022).mkv", "movies/Echo (2022)/Echo (2022).mkv", "movies/Foxtrot (2023)/Foxtrot (2023).mkv",
	"movies/Golf (2018)/Golf (2018).mkv", "movies/Hotel (2017)/Hotel (2017).mkv", "movies/India (2016)/India (2016).mkv",
	"movies/Juliet (2005)/Juliet (2005).mkv", "movies/Kilo (2015)/Kilo (2015).mp4", "movies/Lima (2014)/Lima (2014).mkv",
	"tv/Mike Show/Season 01/Mike Show - S01E01.mkv", "tv/Mike Show/Season 01/Mike Show - S01E02.mkv",
	"tv/Mike Show/Season 01/Mike Show - S01E03.mkv", "tv/November Show/Season 01/November Show - S01E01.mkv",
	"tv/November Show/Season 01/November Show - S01E02.mkv",
}

// libraryTestProber answers from the probe fixtures instead of running ffprobe.
type libraryTestProber struct{}

func libraryTestFixture(base string) (string, bool) {
	for prefix, fx := range libraryTestFixtures {
		if strings.HasPrefix(base, prefix) {
			return filepath.Join("..", "media", "testdata", "probe", fx), true
		}
	}
	return "", false
}

func (libraryTestProber) Probe(_ context.Context, path string) ([]byte, []byte, error) {
	fx, ok := libraryTestFixture(filepath.Base(path))
	if !ok {
		return nil, nil, errors.New("Invalid data found when processing input")
	}
	probe, err := os.ReadFile(fx + ".json")
	if err != nil {
		return nil, nil, err
	}
	frames, _ := os.ReadFile(fx + ".frames.json")
	return probe, frames, nil
}

type libraryTestEncoders struct{}

func (libraryTestEncoders) Select(media.Codec, bool, string) (string, bool, string) {
	return "x265", true, ""
}

// libraryTestEnv is a web server over a synced fixture library.
type libraryTestEnv struct {
	srv  *Server
	h    http.Handler
	st   *store.Store
	lib  *library.Service
	jf   *jellyfintest.Server
	root string
}

func libraryTestMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func libraryTestFilesOnDisk(t *testing.T, root string) {
	t.Helper()
	for _, rel := range libraryTestFiles {
		p := filepath.Join(root, rel)
		libraryTestMust(t, os.MkdirAll(filepath.Dir(p), 0o755))
		libraryTestMust(t, os.WriteFile(p, []byte(rel), 0o644))
		// Sparse-extend to the real clip's size so estimates behave.
		if fx, ok := libraryTestFixture(filepath.Base(p)); ok {
			b, err := os.ReadFile(fx + ".json")
			libraryTestMust(t, err)
			f, err := media.Parse(b, nil, 0)
			libraryTestMust(t, err)
			libraryTestMust(t, os.Truncate(p, f.Size))
		}
	}
}

// newLibraryTestEnv syncs the fixture library with every starter policy on.
func newLibraryTestEnv(t *testing.T) libraryTestEnv {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir())
	libraryTestMust(t, err)
	t.Cleanup(func() { _ = st.Close() })
	jf := jellyfintest.New(t, jellyfintest.WithFixtures())
	root := t.TempDir()
	libraryTestFilesOnDisk(t, root)
	now := func() time.Time { return time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC) }
	lib := library.New(library.Options{Store: st, Prober: libraryTestProber{}, Encoders: libraryTestEncoders{}, Now: now})
	_, err = lib.SaveConnection(ctx, jf.URL, jf.APIKey())
	libraryTestMust(t, err)
	libraryTestMust(t, lib.RefreshServerInfo(ctx))
	libs, err := st.Libraries(ctx)
	libraryTestMust(t, err)
	var ids []string
	for _, l := range libs {
		ids = append(ids, l.ID)
	}
	libraryTestMust(t, st.SetManagedLibraries(ctx, ids))
	libraryTestMust(t, st.SetSelectedUsers(ctx, []string{jellyfintest.FixtureUserID}))
	libraryTestMust(t, st.ReplacePathMappings(ctx, []store.PathMapping{
		{JellyfinPrefix: "/media/movies", LocalPrefix: filepath.ToSlash(filepath.Join(root, "movies"))},
		{JellyfinPrefix: "/media/tv", LocalPrefix: filepath.ToSlash(filepath.Join(root, "tv"))},
	}))
	libraryTestMust(t, lib.CreateStarterPolicies(ctx, nil))
	ps, err := lib.Policies(ctx)
	libraryTestMust(t, err)
	for _, p := range ps {
		libraryTestMust(t, st.SetPolicyEnabled(ctx, p.ID, true))
	}
	libraryTestMust(t, st.SetSetting(ctx, store.KeySetupComplete, "true"))
	_, err = lib.Run(ctx)
	libraryTestMust(t, err)
	srv := New(Deps{Store: st, Library: lib, Now: now})
	return libraryTestEnv{srv: srv, h: srv.Handler(), st: st, lib: lib, jf: jf, root: root}
}

// get fetches a path and checks that nothing secret leaked into the body.
func (e libraryTestEnv) get(t *testing.T, path string, hx bool) (*http.Response, string) {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	if hx {
		req.Header.Set("HX-Request", "true")
		req.Header.Set("HX-Target", "library-results")
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	res := rec.Result()
	body, _ := io.ReadAll(res.Body)
	if bytes.Contains(body, []byte(e.jf.APIKey())) {
		t.Fatalf("%s: the API key appears in the response", path)
	}
	if bytes.Contains(body, []byte(e.jf.URL)) {
		t.Fatalf("%s: the Jellyfin address appears in the response", path)
	}
	return res, string(body)
}

func (e libraryTestEnv) itemID(t *testing.T, name string) string {
	t.Helper()
	rows, _, err := e.st.LibraryList(context.Background(), store.LibraryFilter{Search: name, Limit: 1})
	libraryTestMust(t, err)
	if len(rows) == 0 {
		t.Fatalf("item %q not found", name)
	}
	return rows[0].Item.ID
}

func TestLibraryFilters(t *testing.T) {
	e := newLibraryTestEnv(t)
	cases := []struct {
		query   string
		want    []string
		notWant []string
	}{
		{"", []string{"Alpha (2019)", "Mike Show · S01E02", "Showing 1 to 17 of 17"}, []string{"S01E02 · Mike Show", "(2019) (2019)"}},
		{"?outcome=protected", []string{"Bravo (2020)", "Golf (2018)", `<span class="missing">Not planned</span>`}, []string{"Alpha (2019)", "Juliet (2005)"}},
		{"?outcome=protected&outcome=skipped", []string{"Bravo (2020)", "Juliet (2005)"}, []string{"Alpha (2019)"}},
		{"?codec=av1", []string{"Foxtrot (2023)", "Showing 1 to 1 of 1"}, []string{"Alpha (2019)"}},
		{"?res=2160&hdr=1", []string{"Delta (2022)"}, []string{"Charlie (2021)", "Alpha (2019)"}},
		{"?q=november", []string{"November Show · S01E01"}, []string{"Mike Show"}},
		{"?outcome=optimise", []string{"Alpha (2019)", "1080p H.264 → 1080p HEVC", "Needs optimisation"}, []string{"Bravo (2020)"}},
		{"?outcome=bogus&codec=vp9", []string{"Showing 1 to 17 of 17"}, []string{"Clear filters"}},
		{"?q=nothing-matches-this", []string{"No items match these filters."}, nil},
	}
	for _, c := range cases {
		res, body := e.get(t, "/library"+c.query, false)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d", c.query, res.StatusCode)
		}
		for _, w := range c.want {
			if !strings.Contains(body, templEscape(w)) && !strings.Contains(body, w) {
				t.Errorf("%s: missing %q", c.query, w)
			}
		}
		for _, w := range c.notWant {
			if strings.Contains(body, templEscape(w)) {
				t.Errorf("%s: should not contain %q", c.query, w)
			}
		}
	}
}

// templEscape matches how templ escapes text.
func templEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "'", "&#39;", `"`, "&#34;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func TestLibraryHTMXReturnsResultsOnly(t *testing.T) {
	e := newLibraryTestEnv(t)
	_, body := e.get(t, "/library?outcome=protected&sort=size", true)
	if strings.Contains(body, "<html") || strings.Contains(body, `id="library-filters"`) {
		t.Fatal("HTMX response is a full page")
	}
	if !strings.Contains(body, `id="library-results"`) || !strings.Contains(body, `id="library-filter-count" hx-swap-oob="true"`) {
		t.Fatalf("HTMX response is missing the results or the filter count:\n%s", body)
	}
	if !strings.Contains(body, `aria-sort="descending"`) {
		t.Error("the size column is not marked as sorted")
	}
}

func TestLibraryPaging(t *testing.T) {
	e := newLibraryTestEnv(t)
	// Page 2 of 17 items is empty but still answers.
	res, body := e.get(t, "/library?page=2", false)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "No items match") {
		t.Fatalf("page 2: status %d", res.StatusCode)
	}
	// A merged query honours paging the same way as a single one.
	rows, total, err := e.srv.libraryRows(context.Background(), libraryQuery{Outcomes: []string{"optimise", "protected"}, Sort: "size", Page: 1})
	libraryTestMust(t, err)
	if len(rows) != total || total == 0 {
		t.Fatalf("merged rows %d, total %d", len(rows), total)
	}
	for i := 1; i < len(rows); i++ {
		if rows[i].Size > rows[i-1].Size {
			t.Fatalf("merged rows are not sorted by size at %d", i)
		}
	}
}

func TestItemDetailMultiAudio(t *testing.T) {
	e := newLibraryTestEnv(t)
	res, body := e.get(t, "/library/"+e.itemID(t, "Golf"), false)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	for _, w := range []string{
		"Golf (2018)", "Surround 5.1", "Director's Commentary", "5.1(side)", "Forced", "Commentary",
		"Protected by", "Audio streams", "Subtitle streams", "Watched by", "Jellyfin path", "Inspect again",
	} {
		if !strings.Contains(body, templEscape(w)) {
			t.Errorf("missing %q", w)
		}
	}
	if !strings.Contains(body, `class="mono">fre</td>`) {
		t.Error("the French audio track is missing")
	}
	if !strings.Contains(body, `<span class="missing">None</span>`) {
		t.Error("an empty stream title or flags cell is blank instead of None")
	}
	if !strings.Contains(body, `class="explain__item explain__item--pass"`) {
		t.Error("the winning policy's explanation is missing")
	}
	if strings.Contains(body, "Optimise now") {
		t.Error("a protected item offers Optimise now")
	}
}

func TestItemDetailOptimise(t *testing.T) {
	e := newLibraryTestEnv(t)
	_, body := e.get(t, "/library/"+e.itemID(t, "Alpha"), false)
	for _, w := range []string{
		"Proposed", "Estimated size", "Optimise now", "Dry Run is on. Turn it off in Settings to optimise files.",
		"Preserve", "HEVC", "policies did not match", "Mbps, estimated from the container bitrate",
	} {
		if !strings.Contains(body, templEscape(w)) {
			t.Errorf("missing %q", w)
		}
	}
	if !strings.Contains(body, `disabled aria-describedby="item-optimise-note"`) {
		t.Error("Optimise now is not disabled with its reason")
	}

	_, body = e.get(t, "/library/"+e.itemID(t, "Juliet"), false)
	if !strings.Contains(body, "Skipped") || !strings.Contains(body, "explain__item--fail") {
		t.Error("a skipped item does not list its reasons")
	}
	_, body = e.get(t, "/library/"+e.itemID(t, "Delta"), false)
	if !strings.Contains(body, "HDR10") || !strings.Contains(body, "How the dynamic range was worked out") {
		t.Error("HDR facts are missing")
	}

	res, _ := e.get(t, "/library/no-such-item", false)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown item: status %d", res.StatusCode)
	}
}

func TestItemReprobe(t *testing.T) {
	e := newLibraryTestEnv(t)
	id := e.itemID(t, "Alpha")
	req := httptest.NewRequest("POST", "/library/"+id+"/reprobe", nil)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/library/"+id) {
		t.Fatalf("reprobe: %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	libraryTestWaitIdle(t, e.lib)
	if _, err := e.st.Probe(context.Background(), id); err != nil {
		t.Fatalf("the item was not inspected again: %v", err)
	}
}

func libraryTestWaitIdle(t *testing.T, lib *library.Service) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for lib.Status().Running {
		if time.Now().After(deadline) {
			t.Fatal("the background run did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestImageProxy(t *testing.T) {
	e := newLibraryTestEnv(t)
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDRfake-image-bytes")
	e.jf.AddItem(jellyfintest.Item{
		ID: "0123456789abcdef0123456789abcdef", Name: "Zulu", Type: "Movie", ProductionYear: 2024,
		Path: "/media/movies/Zulu (2024)/Zulu (2024).mkv", Container: "mkv", Image: png,
	})
	_, err := e.lib.Run(context.Background())
	libraryTestMust(t, err)

	res, body := e.get(t, "/img/0123456789abcdef0123456789abcdef?w=5000", false)
	if res.StatusCode != http.StatusOK || body != string(png) {
		t.Fatalf("status %d, body %q", res.StatusCode, body)
	}
	if ct := res.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("content type %q", ct)
	}
	if cc := res.Header.Get("Cache-Control"); cc != "private, max-age=86400" {
		t.Errorf("cache control %q", cc)
	}
	reqs := e.jf.RequestsFor("/Items/0123456789abcdef0123456789abcdef/Images/Primary")
	if len(reqs) != 1 || reqs[0].Query.Get("maxWidth") != "600" {
		t.Fatalf("image requests %+v", reqs)
	}

	for _, p := range []string{"/img/not-in-the-store", "/img/" + e.itemID(t, "Alpha")} {
		if res, _ := e.get(t, p, false); res.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", p, res.StatusCode)
		}
	}

	_, body = e.get(t, "/library?q=Zulu", false)
	if !strings.Contains(body, `src="/img/0123456789abcdef0123456789abcdef?w=80"`) || !strings.Contains(body, `loading="lazy"`) {
		t.Error("the Library does not show the artwork through /img")
	}
}

func TestLibraryImageWidth(t *testing.T) {
	for in, want := range map[string]int{"": 200, "x": 200, "10": 40, "80": 80, "5000": 600} {
		if got := libraryImageWidth(in); got != want {
			t.Errorf("width %q: %d, want %d", in, got, want)
		}
	}
}

func TestLibraryTitle(t *testing.T) {
	year, season, episode := 2019, 1, 2
	cases := []struct {
		name string
		it   store.Item
		want string
	}{
		{"film with a year", store.Item{Name: "Alpha", Year: &year}, "Alpha (2019)"},
		{"film named after its folder", store.Item{Name: "Alpha (2019)", Year: &year}, "Alpha (2019)"},
		{"episode", store.Item{Name: "Pilot", SeriesName: "Show", SeasonNumber: &season, EpisodeNumber: &episode}, "Show · S01E02 · Pilot"},
		{"episode named after its file", store.Item{Name: "Show - S01E02", SeriesName: "Show", SeasonNumber: &season, EpisodeNumber: &episode}, "Show · S01E02"},
	}
	for _, c := range cases {
		if got := libraryTitle(c.it); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
