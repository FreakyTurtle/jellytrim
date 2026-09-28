package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/freakyturtle/jellytrim/internal/store"
	"github.com/freakyturtle/jellytrim/internal/version"
)

func newTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(Deps{Store: st}), st
}

func do(t *testing.T, h http.Handler, req *http.Request) *http.Response {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func TestHealthz(t *testing.T) {
	s, _ := newTestServer(t)
	res := do(t, s.Handler(), httptest.NewRequest("GET", "/healthz", nil))
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	res = do(t, s.Handler(), httptest.NewRequest("GET", "/readyz", nil))
	if res.StatusCode != http.StatusOK {
		t.Fatalf("readyz status %d", res.StatusCode)
	}
}

func TestFirstRunRedirectsToSetup(t *testing.T) {
	s, _ := newTestServer(t)
	res := do(t, s.Handler(), httptest.NewRequest("GET", "/library", nil))
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/setup" {
		t.Fatalf("got %d to %q, want redirect to /setup", res.StatusCode, res.Header.Get("Location"))
	}
}

func TestHTMXRedirectUsesHeader(t *testing.T) {
	s, _ := newTestServer(t)
	req := httptest.NewRequest("GET", "/library", nil)
	req.Header.Set("HX-Request", "true")
	res := do(t, s.Handler(), req)
	if res.Header.Get("HX-Redirect") != "/setup" {
		t.Fatalf("HX-Redirect = %q", res.Header.Get("HX-Redirect"))
	}
}

func TestPagesRenderAfterSetup(t *testing.T) {
	s, st := newTestServer(t)
	if err := st.SetSetting(context.Background(), store.KeySetupComplete, "true"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/", "/library", "/policies", "/queue", "/history", "/settings", "/styleguide"} {
		res := do(t, s.Handler(), httptest.NewRequest("GET", p, nil))
		body, _ := io.ReadAll(res.Body)
		if res.StatusCode != http.StatusOK {
			t.Errorf("%s: status %d", p, res.StatusCode)
		}
		if !strings.Contains(string(body), `class="wordmark"`) {
			t.Errorf("%s: layout missing", p)
		}
	}
}

func TestCrossOriginPostRejected(t *testing.T) {
	s, _ := newTestServer(t)
	req := httptest.NewRequest("POST", "/setup", strings.NewReader("x=1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	res := do(t, s.Handler(), req)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin POST status %d, want 403", res.StatusCode)
	}
}

func TestStaticAssetsServed(t *testing.T) {
	s, _ := newTestServer(t)
	for _, p := range []string{"/static/vendor/htmx.min.js", "/static/css/tokens.css"} {
		res := do(t, s.Handler(), httptest.NewRequest("GET", p+"?v=1", nil))
		if res.StatusCode != http.StatusOK {
			t.Errorf("%s: status %d", p, res.StatusCode)
		}
		if !strings.Contains(res.Header.Get("Cache-Control"), "immutable") {
			t.Errorf("%s: versioned asset not cached", p)
		}
	}
}

func TestAssetVersionChangesWithEachBuild(t *testing.T) {
	oldV, oldD := version.Version, version.Date
	defer func() { version.Version, version.Date = oldV, oldD }()
	version.Version = "dev-stack"
	version.Date = "2026-09-28T14:27:42Z"
	a := assetVersion()
	version.Date = "2026-09-28T17:42:43Z"
	if b := assetVersion(); a == b || !strings.HasPrefix(b, "dev-stack-") {
		t.Fatalf("builds with the same version share a cache key: %q %q", a, b)
	}
}
