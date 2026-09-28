package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/freakyturtle/jellytrim/internal/store"
)

func TestDashboardMetricsAndSummary(t *testing.T) {
	e := newLibraryTestEnv(t)
	sum, err := e.lib.DryRun(context.Background())
	libraryTestMust(t, err)
	res, body := e.get(t, "/", false)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	for _, w := range []string{
		"Media storage", "JellyTrim saved", "Optimisable", "Queued", "No files optimised yet.",
		"17 items in 2 libraries", "items evaluated", "Current size", "Estimated after", "Estimated saving",
		"Estimates are based on each file", "Sync now", "Problems", "skipped for safety", "Juliet (2005)",
	} {
		if !strings.Contains(body, templEscape(w)) {
			t.Errorf("missing %q", w)
		}
	}
	if len(sum.Lines) == 0 {
		t.Fatal("the fixture summary has no lines")
	}
	for _, l := range sum.Lines {
		if !strings.Contains(body, templEscape(l.Text)) {
			t.Errorf("summary line %q missing", l.Text)
		}
	}
	if !strings.Contains(body, `href="/library?outcome=optimise"`) {
		t.Error("summary lines do not link to the Library")
	}
	if !strings.Contains(body, "Estimate.") {
		t.Error("the Optimisable metric is not labelled as an estimate")
	}
}

func TestDashboardWithoutItems(t *testing.T) {
	s, st := newTestServer(t)
	libraryTestMust(t, st.SetSetting(context.Background(), store.KeySetupComplete, "true"))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	body, _ := io.ReadAll(rec.Result().Body)
	if rec.Code != http.StatusOK || !strings.Contains(string(body), "No items yet") {
		t.Fatalf("status %d; empty state missing", rec.Code)
	}
	if !strings.Contains(string(body), "Sync not run yet.") {
		t.Error("metrics do not say why they are unknown")
	}
}

func TestSyncNow(t *testing.T) {
	e := newLibraryTestEnv(t)

	// A plain form post starts a run and goes back to the Dashboard.
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, httptest.NewRequest("POST", "/sync", nil))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("form post: %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	libraryTestWaitIdle(t, e.lib)

	// HTMX gets the status fragment, which polls while the run lasts.
	req := httptest.NewRequest("POST", "/sync", nil)
	req.Header.Set("HX-Request", "true")
	rec = httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || strings.Contains(body, "<html") {
		t.Fatalf("HTMX post: status %d", rec.Code)
	}
	if !strings.Contains(body, `hx-get="/sync/status?was=running"`) && !strings.Contains(body, "Sync now") {
		t.Fatalf("unexpected fragment:\n%s", body)
	}
	libraryTestWaitIdle(t, e.lib)

	// Once idle, the fragment stops polling and asks the page to refresh.
	res, body := e.get(t, "/sync/status?was=running", false)
	if strings.Contains(body, "hx-trigger") {
		t.Error("the idle fragment still polls")
	}
	if res.Header.Get("HX-Trigger") != dashRefreshEvent {
		t.Errorf("HX-Trigger %q", res.Header.Get("HX-Trigger"))
	}
	res, body = e.get(t, "/dashboard/live", false)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `id="dash-live"`) || strings.Contains(body, "<html") {
		t.Fatalf("live fragment: status %d", res.StatusCode)
	}
}

func TestLibraryCount(t *testing.T) {
	for n, want := range map[int]string{0: "0", 12: "12", 999: "999", 1000: "1,000", 1432: "1,432", 1234567: "1,234,567"} {
		if got := libraryCount(n); got != want {
			t.Errorf("%d: %q, want %q", n, got, want)
		}
	}
	if got := dashRange(1_200_000_000_000, 1_500_000_000_000); got != "1.2 to 1.5 TB" {
		t.Errorf("range %q", got)
	}
	if got := dashRange(900_000_000_000, 1_100_000_000_000); got != "900 GB to 1.1 TB" {
		t.Errorf("range %q", got)
	}
}
