package web

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/freakyturtle/jellytrim/internal/jellyfin/jellyfintest"
	"github.com/freakyturtle/jellytrim/internal/library"
	"github.com/freakyturtle/jellytrim/internal/store"
)

// setupFailingProber answers every probe with an error, as ffprobe does for
// a file it cannot read. The sync records the failure and carries on.
type setupFailingProber struct{}

func (setupFailingProber) Probe(context.Context, string) ([]byte, []byte, error) {
	return nil, nil, errors.New("Invalid data found when processing input")
}

// setupEnv is a server wired to a real store, a fake Jellyfin and a real
// library service. Every response body is kept so tests can check that the
// API key never reaches the browser.
type setupEnv struct {
	t      *testing.T
	s      *Server
	h      http.Handler
	st     *store.Store
	jf     *jellyfintest.Server
	lib    *library.Service
	root   string
	bodies []string
}

func newSetupEnv(t *testing.T) *setupEnv {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	jf := jellyfintest.New(t, jellyfintest.WithFixtures())
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	lib := library.New(library.Options{Store: st, Prober: setupFailingProber{}, Log: log})
	lib.Start(ctx)
	root := t.TempDir()
	for _, rel := range []string{"movies/Alpha (2019)/Alpha (2019).mkv", "tv/Mike Show/Season 01/Mike Show - S01E01.mkv"} {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := New(Deps{Store: st, Library: lib, Log: log})
	e := &setupEnv{t: t, s: s, h: s.Handler(), st: st, jf: jf, lib: lib, root: root}
	t.Cleanup(func() {
		e.waitIdle()
		cancel()
		_ = st.Close()
	})
	return e
}

// waitIdle waits for a background sync or evaluation to finish.
func (e *setupEnv) waitIdle() {
	e.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for e.lib.Status().Running {
		if time.Now().After(deadline) {
			e.t.Fatal("background run did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (e *setupEnv) do(req *http.Request) (*http.Response, string) {
	e.t.Helper()
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	res := rec.Result()
	body := rec.Body.String()
	e.bodies = append(e.bodies, req.Method+" "+req.URL.Path+"\n"+body)
	return res, body
}

func (e *setupEnv) get(path string, htmx bool) (*http.Response, string) {
	e.t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	return e.do(req)
}

func (e *setupEnv) post(path string, form url.Values, htmx bool) (*http.Response, string) {
	e.t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	return e.do(req)
}

// expectRedirect checks a 303 to the given location.
func (e *setupEnv) expectRedirect(res *http.Response, body, to string) {
	e.t.Helper()
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != to {
		e.t.Fatalf("got %d to %q, want redirect to %s\n%s", res.StatusCode, res.Header.Get("Location"), to, body)
	}
}

func expectStatus(t *testing.T, res *http.Response, body string, want int) {
	t.Helper()
	if res.StatusCode != want {
		t.Fatalf("status %d, want %d\n%s", res.StatusCode, want, body)
	}
}

func expectContains(t *testing.T, body string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("body does not contain %q", w)
		}
	}
}

// checkNoKey fails if the API key appears in any response so far.
func (e *setupEnv) checkNoKey() {
	e.t.Helper()
	for _, b := range e.bodies {
		if strings.Contains(b, e.jf.APIKey()) {
			e.t.Fatalf("the API key was rendered in:\n%s", b[:strings.Index(b, "\n")])
		}
	}
}

// connect walks the first two steps and returns the library and user IDs.
func (e *setupEnv) connect() (movies, tv, user string) {
	e.t.Helper()
	res, body := e.post("/setup/jellyfin", url.Values{"url": {e.jf.URL}, "key": {e.jf.APIKey()}}, false)
	e.expectRedirect(res, body, "/setup/libraries")
	ctx := context.Background()
	libs, err := e.st.Libraries(ctx)
	if err != nil || len(libs) != 2 {
		e.t.Fatalf("libraries after connecting: %v %v", libs, err)
	}
	for _, l := range libs {
		switch l.CollectionType {
		case "movies":
			movies = l.ID
		case "tvshows":
			tv = l.ID
		}
	}
	users, err := e.st.Users(ctx)
	if err != nil || len(users) == 0 {
		e.t.Fatalf("users after connecting: %v %v", users, err)
	}
	return movies, tv, users[0].ID
}

func (e *setupEnv) mappingForm() url.Values {
	return url.Values{
		"jellyfin": {"/media/movies", "/media/tv"},
		"local":    {filepath.Join(e.root, "movies"), filepath.Join(e.root, "tv")},
	}
}

func TestSetupWizardEndToEnd(t *testing.T) {
	e := newSetupEnv(t)
	ctx := context.Background()

	res, body := e.get("/setup", false)
	e.expectRedirect(res, body, "/setup/welcome")
	res, body = e.get("/setup/welcome", false)
	expectStatus(t, res, body, http.StatusOK)
	expectContains(t, body, "Dry Run is on", "Start setup", `aria-current="step"`)
	res, body = e.post("/setup/welcome", nil, false)
	e.expectRedirect(res, body, "/setup/jellyfin")

	// Test the connection without saving it.
	res, body = e.post("/setup/jellyfin/test", url.Values{"url": {e.jf.URL}, "key": {e.jf.APIKey()}}, true)
	expectStatus(t, res, body, http.StatusOK)
	expectContains(t, body, "Connected to Jellyfin 12.1.0 on jellytrim-dev.")
	if st, _ := e.st.Settings(ctx); st.HasAPIKey {
		t.Fatal("testing the connection saved the key")
	}

	movies, tv, user := e.connect()
	res, body = e.get("/setup/libraries", false)
	expectStatus(t, res, body, http.StatusOK)
	expectContains(t, body, "Movies", "/media/tv", "dev", `name="watch_mode"`)

	res, body = e.post("/setup/libraries", url.Values{"library": {movies, tv}, "user": {user}, "watch_mode": {"all"}}, false)
	e.expectRedirect(res, body, "/setup/paths")
	libs, _ := e.st.Libraries(ctx)
	for _, l := range libs {
		if !l.Managed {
			t.Errorf("library %s not managed", l.Name)
		}
	}
	if st, _ := e.st.Settings(ctx); st.WatchMode != "all" {
		t.Errorf("watch mode %q", st.WatchMode)
	}

	res, body = e.get("/setup/paths", false)
	expectStatus(t, res, body, http.StatusOK)
	expectContains(t, body, `value="/media/movies"`, `value="/media/tv"`)

	res, body = e.post("/setup/paths/check", e.mappingForm(), true)
	expectStatus(t, res, body, http.StatusOK)
	expectContains(t, body, "Found 1 of ", "are missing, for example:", "Mike Show - S01E02.mkv")
	res, body = e.post("/setup/paths", e.mappingForm(), false)
	e.expectRedirect(res, body, "/setup/hardware")
	ms, _ := e.st.PathMappings(ctx)
	if len(ms) != 2 || ms[0].LocalPrefix != filepath.Join(e.root, "movies") {
		t.Fatalf("mappings %+v", ms)
	}

	res, body = e.get("/setup/hardware", false)
	expectStatus(t, res, body, http.StatusOK)
	expectContains(t, body, "The hardware test is not available yet")
	res, body = e.post("/setup/hardware", nil, false)
	e.expectRedirect(res, body, "/setup/defaults")

	res, body = e.post("/setup/defaults", url.Values{"quality": {"balanced"}, "codec": {"hevc"}, "encoder": {"software"}}, false)
	e.expectRedirect(res, body, "/setup/policies")
	if st, _ := e.st.Settings(ctx); st.DefaultQuality != "balanced" || st.DefaultCodec != "hevc" || st.EncoderPreference != "software" {
		t.Errorf("defaults %q %q %q", st.DefaultQuality, st.DefaultCodec, st.EncoderPreference)
	}

	res, body = e.get("/setup/policies", false)
	expectStatus(t, res, body, http.StatusOK)
	expectContains(t, body, "Protect favourites", "Efficient encoding", "Archive watched 4K", "Space-saving television", "never change these files")
	res, body = e.post("/setup/policies", url.Values{"enabled": {"Protect favourites", "Efficient encoding"}, "tv": {tv, movies}}, false)
	e.expectRedirect(res, body, "/setup/scan")
	checkStarterPolicies(t, e, tv)

	res, body = e.get("/setup", false)
	e.expectRedirect(res, body, "/setup/scan")
	_, body = e.get("/setup/scan", false)
	expectContains(t, body, "Start Dry Run scan")

	res, body = e.post("/setup/scan", nil, false)
	e.expectRedirect(res, body, "/setup/scan")
	if st, _ := e.st.Settings(ctx); !st.SetupComplete {
		t.Fatal("setup not marked complete")
	}
	e.waitIdle()
	res, body = e.get("/setup/scan", true)
	expectStatus(t, res, body, http.StatusOK)
	expectContains(t, body, "Dry Run scan complete", "Go to Dashboard")
	if strings.Contains(body, "<html") || strings.Contains(body, "hx-trigger") {
		t.Error("finished scan fragment should be a fragment without polling")
	}

	res, body = e.get("/setup/jellyfin", false)
	e.expectRedirect(res, body, "/")
	res, body = e.get("/setup", false)
	e.expectRedirect(res, body, "/")
	e.checkNoKey()
}

func checkStarterPolicies(t *testing.T, e *setupEnv, tv string) {
	t.Helper()
	ps, err := e.lib.Policies(context.Background())
	if err != nil || len(ps) != 4 {
		t.Fatalf("policies %v %v", ps, err)
	}
	want := map[string]bool{"Protect favourites": true, "Efficient encoding": true, "Archive watched 4K": false, "Space-saving television": false}
	for _, p := range ps {
		if p.Enabled != want[p.Name] {
			t.Errorf("%s enabled = %v", p.Name, p.Enabled)
		}
		// Only managed show libraries can be chosen for the TV policy.
		if p.Name == "Space-saving television" && (len(p.Scope.Libraries) != 1 || p.Scope.Libraries[0] != tv) {
			t.Errorf("TV policy libraries %v", p.Scope.Libraries)
		}
	}
}

func TestSetupRunningScanPolls(t *testing.T) {
	v := setupRunningView("Inspecting files", 612, 1204)
	if v.Percent != 50 || v.Readout != "612 of 1,204" {
		t.Fatalf("%+v", v)
	}
	if v := setupRunningView("", 0, 0); v.Phase != "Connecting to Jellyfin" || v.Percent != -1 {
		t.Fatalf("%+v", v)
	}
}

func TestSetupJellyfinValidation(t *testing.T) {
	e := newSetupEnv(t)
	res, body := e.post("/setup/jellyfin", url.Values{"url": {"jellyfin:8096"}, "key": {""}}, false)
	expectStatus(t, res, body, http.StatusUnprocessableEntity)
	expectContains(t, body, `aria-invalid="true"`, "Enter an address starting with http://", "Enter an API key.")

	res, body = e.post("/setup/jellyfin", url.Values{"url": {e.jf.URL}, "key": {"not-the-right-key"}}, false)
	expectStatus(t, res, body, http.StatusUnprocessableEntity)
	expectContains(t, body, "Jellyfin rejected this API key", `role="alert"`)
	if strings.Contains(body, "not-the-right-key") {
		t.Fatal("the rejected key was rendered")
	}

	// A key typed into the field is never sent back.
	res, body = e.post("/setup/jellyfin/test", url.Values{"url": {"http://127.0.0.1:1"}, "key": {e.jf.APIKey()}}, false)
	expectStatus(t, res, body, http.StatusOK)
	expectContains(t, body, "JellyTrim could not reach http://127.0.0.1:1")
	e.checkNoKey()
}

func TestSetupSavedKeyIsNeverRendered(t *testing.T) {
	e := newSetupEnv(t)
	e.connect()
	// Going back to the step shows that a key is saved, with an empty field.
	res, body := e.get("/setup/jellyfin", false)
	expectStatus(t, res, body, http.StatusOK)
	expectContains(t, body, "An API key is saved", `value="`+e.jf.URL+`"`)
	// Continuing with an empty key keeps the saved one.
	res, body = e.post("/setup/jellyfin", url.Values{"url": {e.jf.URL}}, false)
	e.expectRedirect(res, body, "/setup/libraries")
	// Testing with an empty key tests the saved one.
	_, body = e.post("/setup/jellyfin/test", url.Values{"url": {e.jf.URL}}, true)
	expectContains(t, body, "Connected to Jellyfin")
	e.checkNoKey()
}

func TestSetupStartResumes(t *testing.T) {
	e := newSetupEnv(t)
	ctx := context.Background()
	if err := e.st.SetSetting(ctx, store.KeyJellyfinURL, e.jf.URL); err != nil {
		t.Fatal(err)
	}
	res, body := e.get("/setup", false)
	e.expectRedirect(res, body, "/setup/jellyfin")
	movies, _, user := e.connect()
	res, body = e.get("/setup", false)
	e.expectRedirect(res, body, "/setup/libraries")
	e.post("/setup/libraries", url.Values{"library": {movies}, "user": {user}, "watch_mode": {"any"}}, false)
	res, body = e.get("/setup", false)
	e.expectRedirect(res, body, "/setup/paths")
	e.post("/setup/paths", e.mappingForm(), false)
	res, body = e.get("/setup", false)
	e.expectRedirect(res, body, "/setup/hardware")
}

func TestSetupLibrariesValidation(t *testing.T) {
	e := newSetupEnv(t)
	e.connect()
	res, body := e.post("/setup/libraries", url.Values{"library": {"not-a-library"}, "watch_mode": {"sometimes"}}, false)
	expectStatus(t, res, body, http.StatusUnprocessableEntity)
	expectContains(t, body, "Choose at least one library.", "Choose at least one person")
	libs, _ := e.st.Libraries(context.Background())
	for _, l := range libs {
		if l.Managed {
			t.Errorf("%s managed after a failed save", l.Name)
		}
	}
}

func TestSetupPathRowsEditAndValidate(t *testing.T) {
	e := newSetupEnv(t)
	e.connect()
	form := e.mappingForm()
	form.Set("add", "1")
	res, body := e.post("/setup/paths", form, false)
	expectStatus(t, res, body, http.StatusOK)
	if n := strings.Count(body, `name="local"`); n != 3 {
		t.Fatalf("%d rows after adding, want 3", n)
	}

	form = e.mappingForm()
	form.Set("remove", "0")
	_, body = e.post("/setup/paths", form, false)
	if n := strings.Count(body, `name="local"`); n != 1 || !strings.Contains(body, `value="/media/tv"`) {
		t.Fatalf("%d rows after removing the first", n)
	}

	res, body = e.post("/setup/paths", url.Values{"jellyfin": {"/media/movies", "/media/movies/"}, "local": {"relative/path", "/srv"}}, false)
	expectStatus(t, res, body, http.StatusUnprocessableEntity)
	expectContains(t, body, "Enter a full path starting with /", `aria-invalid="true"`)

	res, body = e.post("/setup/paths", url.Values{"jellyfin": {"/media/movies", "/media/movies/"}, "local": {"/a", "/b"}}, false)
	expectStatus(t, res, body, http.StatusUnprocessableEntity)
	expectContains(t, body, "already mapped in another row")

	res, body = e.post("/setup/paths", url.Values{"jellyfin": {""}, "local": {""}}, false)
	expectStatus(t, res, body, http.StatusUnprocessableEntity)
	expectContains(t, body, "Add at least one mapping.")
	if ms, _ := e.st.PathMappings(context.Background()); len(ms) != 0 {
		t.Fatalf("invalid mappings saved: %+v", ms)
	}
}

func TestSetupPathCheckReportsMissingFolder(t *testing.T) {
	e := newSetupEnv(t)
	e.connect()
	form := url.Values{"jellyfin": {"/media/movies"}, "local": {filepath.Join(e.root, "nowhere")}}
	res, body := e.post("/setup/paths/check", form, true)
	expectStatus(t, res, body, http.StatusOK)
	expectContains(t, body, "setup-results__item--bad", "cannot see a folder")
	// Without JavaScript the whole step comes back with the results.
	res, body = e.post("/setup/paths/check", form, false)
	expectStatus(t, res, body, http.StatusOK)
	expectContains(t, body, "<html", "cannot see a folder", `value="/media/movies"`)
}

func TestSetupDefaultsRejectsPlannedCodec(t *testing.T) {
	e := newSetupEnv(t)
	res, body := e.post("/setup/defaults", url.Values{"quality": {"high"}, "codec": {"av1"}, "encoder": {"hardware"}}, false)
	expectStatus(t, res, body, http.StatusUnprocessableEntity)
	expectContains(t, body, "Choose a quality, a codec and an encoder.")
}

func TestSetupUnknownStep(t *testing.T) {
	e := newSetupEnv(t)
	res, body := e.get("/setup/nope", false)
	expectStatus(t, res, body, http.StatusNotFound)
}

func TestSetupCount(t *testing.T) {
	for n, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 1204: "1,204", 1234567: "1,234,567"} {
		if got := setupCount(n); got != want {
			t.Errorf("setupCount(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestSetupDisplayURLDropsSecrets(t *testing.T) {
	got := setupDisplayURL("http://user:pass@jellyfin:8096/jf?api_key=secret#x")
	if got != "http://jellyfin:8096/jf" {
		t.Fatalf("got %q", got)
	}
}
