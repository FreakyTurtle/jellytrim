package jellyfin_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/freakyturtle/jellytrim/internal/jellyfin"
	"github.com/freakyturtle/jellytrim/internal/jellyfin/jellyfintest"
)

// leakKey is distinctive so any echo of it is easy to find. It is not hex,
// so the public check does not mistake it for a real key.
const leakKey = "leak-canary-key-must-never-appear"

// TestKeyNeverAppearsInErrorsLogsOrURLs drives every error path the client
// has and checks the key is in none of the errors, log lines or request URLs.
func TestKeyNeverAppearsInErrorsLogsOrURLs(t *testing.T) {
	var errs []error
	var logs []string
	var urls []string
	record := func(h *harness, err error) {
		if err == nil {
			t.Errorf("expected an error")
			return
		}
		errs = append(errs, err)
		logs = append(logs, h.logs.String())
		if h.srv != nil {
			for _, r := range h.srv.Requests() {
				urls = append(urls, r.Path+"?"+r.Query.Encode())
			}
		}
	}
	ctx := context.Background()
	serverWithKey := func() *harness {
		srv := jellyfintest.New(t, jellyfintest.WithFixtures(), jellyfintest.WithToken(leakKey))
		return newHarnessFor(t, srv, srv.URL, leakKey)
	}

	// Wrong key: the key the client sent must not come back.
	{
		srv := jellyfintest.New(t)
		h := newHarnessFor(t, srv, srv.URL, leakKey)
		_, err := h.c.Ping(ctx)
		record(h, err)
	}
	// Statuses whose bodies echo the key.
	for _, status := range []int{400, 401, 403, 404, 500, 502} {
		h := serverWithKey()
		h.srv.Inject("/Users", jellyfintest.Fault{Status: status, Body: `bad Token="` + leakKey + `"`})
		_, err := h.c.Users(ctx)
		record(h, err)
	}
	// 503 until attempts run out, with retries logged.
	{
		h := serverWithKey()
		for range 3 {
			h.srv.Inject("/Items", jellyfintest.Fault{Status: 503, RetryAfter: time.Second, Body: leakKey})
		}
		_, err := h.c.AllItems(ctx, jellyfin.ItemQuery{}, func(jellyfin.Item) error { return nil })
		record(h, err)
	}
	// Dropped connections until attempts run out.
	{
		h := serverWithKey()
		for range 3 {
			h.srv.FailNext("/Library/VirtualFolders", 0, 0)
		}
		_, err := h.c.Libraries(ctx)
		record(h, err)
	}
	// A 200 whose body is not JSON but contains the key.
	{
		h := serverWithKey()
		h.srv.Inject("/System/Info", jellyfintest.Fault{Status: 200, Body: leakKey + ` {"x":`})
		_, err := h.c.Ping(ctx)
		record(h, err)
	}
	// Connection refused: url.Error carries the request URL.
	{
		h := newHarnessFor(t, nil, refusedURL(t), leakKey)
		_, err := h.c.Item(ctx, "user", "item")
		record(h, err)
	}
	// DNS failure.
	{
		h := newHarnessFor(t, nil, "http://jellyfin:8096", leakKey, jellyfin.WithHTTPClient(dnsFailure()))
		_, _, err := h.c.Image(ctx, "item", 100)
		record(h, err)
	}
	// Client timeout.
	{
		h := serverWithKey()
		h2 := newHarnessFor(t, h.srv, h.srv.URL, leakKey,
			jellyfin.WithHTTPClient(&http.Client{Timeout: 20 * time.Millisecond}), jellyfin.WithRetry(2, 0, 0))
		h.srv.Delay(time.Second)
		err := h2.c.RefreshItem(ctx, "x")
		record(h2, err)
	}
	// Cancelled context.
	{
		h := serverWithKey()
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		err := h.c.NotifyMediaUpdated(cctx, "/media/movies/a.mkv")
		record(h, err)
	}
	// Keys pasted into the URL are refused without repeating the URL.
	for _, u := range []string{
		"http://" + leakKey + "@jellyfin:8096",
		"http://user:" + leakKey + "@jellyfin:8096",
		"http://jellyfin:8096/?api_key=" + leakKey,
		"http://jellyfin:8096/#" + leakKey,
		leakKey + "://jellyfin",
		"http://jellyfin:8096/\x00" + leakKey,
	} {
		_, err := jellyfin.New(u, leakKey)
		if err == nil {
			t.Errorf("New accepted %q", u)
			continue
		}
		errs = append(errs, err)
	}

	for _, err := range errs {
		if strings.Contains(err.Error(), leakKey) {
			t.Errorf("error contains the API key: %v", err)
		}
		if strings.Contains(strings.ToLower(err.Error()), "api_key=") {
			t.Errorf("error contains an api_key parameter: %v", err)
		}
	}
	for _, l := range logs {
		if strings.Contains(l, leakKey) {
			t.Errorf("log contains the API key: %s", l)
		}
	}
	for _, u := range urls {
		if strings.Contains(u, leakKey) {
			t.Errorf("request URL contains the API key: %s", u)
		}
	}
	if len(errs) < 18 {
		t.Errorf("only %d error paths exercised", len(errs))
	}
}
