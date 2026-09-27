package jellyfin_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/freakyturtle/jellytrim/internal/jellyfin"
	"github.com/freakyturtle/jellytrim/internal/jellyfin/jellyfintest"
)

func TestAuthHeaderIsQuotedMediaBrowserHeader(t *testing.T) {
	h := newHarness(t, fixtures(),
		jellyfin.WithDevice(`living "room" \ box`),
		jellyfin.WithDeviceID("id-1"),
		jellyfin.WithVersion("1.2.3"))
	if _, err := h.c.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	reqs := h.srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("want 1 request, got %d", len(reqs))
	}
	want := `MediaBrowser Client="JellyTrim", Device="living \"room\" \\ box", DeviceId="id-1", Version="1.2.3", Token="` +
		jellyfintest.Token + `"`
	if got := reqs[0].Authorization; got != want {
		t.Fatalf("Authorization header\n got: %s\nwant: %s", got, want)
	}
	fields, ok := jellyfintest.AuthHeaderFields(reqs[0].Authorization)
	if !ok || fields["Device"] != `living "room" \ box` || fields["Token"] != jellyfintest.Token {
		t.Fatalf("header does not round-trip: %v %v", ok, fields)
	}
}

func TestEveryEndpointUsesOnlyTheHeader(t *testing.T) {
	h := newHarness(t, fixtures())
	ctx := context.Background()
	u := jellyfintest.FixtureUserID
	calls := []func() error{
		func() error { _, err := h.c.PublicInfo(ctx); return err },
		func() error { _, err := h.c.Ping(ctx); return err },
		func() error { _, err := h.c.Libraries(ctx); return err },
		func() error { _, err := h.c.Users(ctx); return err },
		func() error { _, err := h.c.Items(ctx, jellyfin.ItemQuery{UserID: u}); return err },
		func() error { _, err := h.c.Collections(ctx, u); return err },
		func() error { _, err := h.c.Item(ctx, u, "d15b890b81018d54a2b0f65fbee75563"); return err },
		func() error { return h.c.RefreshItem(ctx, "d15b890b81018d54a2b0f65fbee75563") },
		func() error { return h.c.NotifyMediaUpdated(ctx, "/media/movies/Alpha (2019)/Alpha (2019).mkv") },
	}
	for i, call := range calls {
		if err := call(); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	for _, r := range h.srv.Requests() {
		if r.Header.Get("X-Emby-Token") != "" {
			t.Errorf("%s %s sent X-Emby-Token", r.Method, r.Path)
		}
		for k, vs := range r.Query {
			lk := strings.ToLower(k)
			if lk == "api_key" || lk == "apikey" || strings.Contains(strings.Join(vs, ","), jellyfintest.Token) {
				t.Errorf("%s %s put the key in the query", r.Method, r.Path)
			}
		}
		if !strings.HasPrefix(r.Authorization, "MediaBrowser ") {
			t.Errorf("%s %s: no MediaBrowser header", r.Method, r.Path)
		}
	}
}

func TestStatusesMapToTypedErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   []error
		notIs  []error
	}{
		{"401 is unauthorised", 401, []error{jellyfin.ErrUnauthorized}, []error{jellyfin.ErrNotFound}},
		{"403 is unauthorised", 403, []error{jellyfin.ErrUnauthorized}, nil},
		{"404 is not found", 404, []error{jellyfin.ErrNotFound}, []error{jellyfin.ErrUnauthorized}},
		{"500 is a plain status error", 500, nil, []error{jellyfin.ErrUnauthorized, jellyfin.ErrNotFound, jellyfin.ErrUnavailable}},
		{"502 is unavailable", 502, []error{jellyfin.ErrUnavailable}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, fixtures())
			h.srv.FailNext("/Library/VirtualFolders", tc.status, 0)
			_, err := h.c.Libraries(context.Background())
			var se *jellyfin.StatusError
			if !errors.As(err, &se) || se.StatusCode != tc.status || se.Path != "/Library/VirtualFolders" {
				t.Fatalf("want StatusError %d, got %v", tc.status, err)
			}
			for _, w := range tc.want {
				if !errors.Is(err, w) {
					t.Errorf("errors.Is(%v, %v) = false", err, w)
				}
			}
			for _, w := range tc.notIs {
				if errors.Is(err, w) {
					t.Errorf("errors.Is(%v, %v) = true", err, w)
				}
			}
		})
	}
}

func TestWrongKeyIsUnauthorisedAndSaysToCheckTheKey(t *testing.T) {
	srv := jellyfintest.New(t)
	h := newHarnessFor(t, srv, srv.URL, "not-the-right-key")
	_, err := h.c.Ping(context.Background())
	if !errors.Is(err, jellyfin.ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
	if !strings.Contains(err.Error(), "check the API key") {
		t.Errorf("message should say to check the key: %v", err)
	}
	if len(h.sleep.got()) != 0 {
		t.Errorf("401 must not be retried")
	}
}

func TestServiceUnavailableHonoursRetryAfter(t *testing.T) {
	h := newHarness(t, fixtures())
	h.srv.FailNext("/System/Info", 503, 7*time.Second)
	h.srv.FailNext("/System/Info", 503, 2*time.Second)
	info, err := h.c.Ping(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.ServerName != jellyfintest.DefaultServerName {
		t.Errorf("server name %q", info.ServerName)
	}
	if got := h.sleep.got(); len(got) != 2 || got[0] != 7*time.Second || got[1] != 2*time.Second {
		t.Errorf("waits = %v, want [7s 2s]", got)
	}
	if n := len(h.srv.RequestsFor("/System/Info")); n != 3 {
		t.Errorf("requests = %d, want 3", n)
	}
}

func TestRetryAfterIsCappedAndAttemptsAreLimited(t *testing.T) {
	h := newHarness(t, fixtures(), jellyfin.WithRetry(3, time.Second, 10*time.Second))
	for range 3 {
		h.srv.FailNext("/Users", 503, time.Hour)
	}
	_, err := h.c.Users(context.Background())
	if !errors.Is(err, jellyfin.ErrUnavailable) {
		t.Fatalf("want ErrUnavailable after the last attempt, got %v", err)
	}
	if got := h.sleep.got(); len(got) != 2 || got[0] != 10*time.Second || got[1] != 10*time.Second {
		t.Errorf("waits = %v, want two capped 10s waits", got)
	}
	if n := len(h.srv.RequestsFor("/Users")); n != 3 {
		t.Errorf("requests = %d, want 3", n)
	}
}

func TestServiceUnavailableWithoutRetryAfterBacksOff(t *testing.T) {
	h := newHarness(t, fixtures(), jellyfin.WithRetry(3, 100*time.Millisecond, time.Minute))
	h.srv.FailNext("/Users", 503, 0)
	h.srv.FailNext("/Users", 503, 0)
	if _, err := h.c.Users(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := h.sleep.got(); len(got) != 2 || got[0] != 100*time.Millisecond || got[1] != 200*time.Millisecond {
		t.Errorf("waits = %v, want [100ms 200ms]", got)
	}
}

func TestDroppedConnectionIsRetriedForGets(t *testing.T) {
	h := newHarness(t, fixtures())
	h.srv.FailNext("/Library/VirtualFolders", 0, 0)
	libs, err := h.c.Libraries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(libs) != 2 {
		t.Errorf("libraries = %d", len(libs))
	}
	if got := h.sleep.got(); len(got) != 1 || got[0] != jellyfin.DefaultBackoff {
		t.Errorf("waits = %v, want one default backoff", got)
	}
}

func TestPostsAreNeverRetried(t *testing.T) {
	h := newHarness(t, fixtures())
	h.srv.FailNext("/Library/Media/Updated", 503, time.Second)
	err := h.c.NotifyMediaUpdated(context.Background(), "/media/movies/a.mkv")
	if !errors.Is(err, jellyfin.ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
	if n := len(h.srv.RequestsFor("/Library/Media/Updated")); n != 1 {
		t.Errorf("requests = %d, want 1", n)
	}
	if len(h.sleep.got()) != 0 {
		t.Errorf("a POST waited to retry")
	}
	if len(h.srv.MediaUpdates()) != 0 {
		t.Errorf("the failed call should not have been recorded")
	}
}

// refusedURL returns a URL on a port nothing listens on.
func refusedURL(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return "http://" + addr
}

func TestConnectionRefusedIsUnavailableWithAHint(t *testing.T) {
	url := refusedURL(t)
	h := newHarnessFor(t, nil, url, jellyfintest.Token)
	_, err := h.c.Ping(context.Background())
	if !errors.Is(err, jellyfin.ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
	var ue *jellyfin.UnavailableError
	if !errors.As(err, &ue) || ue.BaseURL != url {
		t.Fatalf("want *UnavailableError for %s, got %#v", url, err)
	}
	msg := err.Error()
	for _, want := range []string{"could not reach " + url, "Check the URL and that both containers share a network"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q lacks %q", msg, want)
		}
	}
	if strings.Contains(msg, jellyfintest.Token) {
		t.Errorf("message contains the API key")
	}
	if got := h.sleep.got(); len(got) != 2 || got[0] != jellyfin.DefaultBackoff || got[1] != 2*jellyfin.DefaultBackoff {
		t.Errorf("waits = %v, want doubling backoff over 3 attempts", got)
	}
}

// dnsFailure is a transport whose every dial fails name resolution.
func dnsFailure() *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(_ context.Context, _, addr string) (net.Conn, error) {
			host, _, _ := net.SplitHostPort(addr)
			return nil, &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}}
		},
	}}
}

func TestDNSFailureIsUnavailable(t *testing.T) {
	h := newHarnessFor(t, nil, "http://jellyfin:8096", jellyfintest.Token, jellyfin.WithHTTPClient(dnsFailure()))
	_, err := h.c.Libraries(context.Background())
	if !errors.Is(err, jellyfin.ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
	var dnsErr *net.DNSError
	if !errors.As(err, &dnsErr) {
		t.Errorf("the DNS error should stay reachable with errors.As: %v", err)
	}
	if !strings.Contains(err.Error(), "JellyTrim could not reach http://jellyfin:8096. Check the URL") {
		t.Errorf("message: %v", err)
	}
}

func TestTimeoutIsUnavailable(t *testing.T) {
	h := newHarness(t, fixtures(),
		jellyfin.WithHTTPClient(&http.Client{Timeout: 50 * time.Millisecond}),
		jellyfin.WithRetry(1, 0, 0))
	h.srv.Delay(2 * time.Second)
	_, err := h.c.Ping(context.Background())
	if !errors.Is(err, jellyfin.ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
	if errors.Is(err, context.Canceled) {
		t.Errorf("a client timeout is not a cancellation")
	}
}

func TestCancelledContextIsNotUnavailable(t *testing.T) {
	h := newHarness(t, fixtures())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := h.c.Ping(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if errors.Is(err, jellyfin.ErrUnavailable) {
		t.Errorf("cancellation must not look like an outage")
	}
	if n := len(h.srv.Requests()); n != 0 {
		t.Errorf("a cancelled call sent %d requests", n)
	}
}

func TestCancelDuringRetryWaitStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	h := newHarness(t, fixtures(), jellyfin.WithSleep(func(context.Context, time.Duration) error {
		cancel()
		return context.Canceled
	}))
	h.srv.FailNext("/System/Info", 503, time.Second)
	_, err := h.c.Ping(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if n := len(h.srv.RequestsFor("/System/Info")); n != 1 {
		t.Errorf("requests = %d, want 1", n)
	}
}

func TestCancelDuringSlowResponse(t *testing.T) {
	h := newHarness(t, fixtures())
	h.srv.Delay(5 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := h.c.Users(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want context.DeadlineExceeded, got %v", err)
	}
	if len(h.sleep.got()) != 0 {
		t.Errorf("the caller's deadline must not be retried")
	}
}

func TestNewValidatesTheURL(t *testing.T) {
	tests := []struct {
		name, url string
		ok        bool
		wantBase  string
	}{
		{"http with trailing slash", "http://jellyfin:8096/", true, "http://jellyfin:8096"},
		{"https with a path prefix", " https://media.example.org/jellyfin// ", true, "https://media.example.org/jellyfin"},
		{"no scheme", "jellyfin:8096", false, ""},
		{"ftp", "ftp://jellyfin", false, ""},
		{"no host", "http://", false, ""},
		{"user info", "http://admin:secret@jellyfin:8096", false, ""},
		{"query", "http://jellyfin:8096/?api_key=abc", false, ""},
		{"fragment", "http://jellyfin:8096/#x", false, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, err := jellyfin.New(tc.url, "k")
			if tc.ok != (err == nil) {
				t.Fatalf("New(%q) error = %v, want ok=%v", tc.url, err, tc.ok)
			}
			if tc.ok && c.BaseURL() != tc.wantBase {
				t.Errorf("BaseURL = %q, want %q", c.BaseURL(), tc.wantBase)
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Errorf("error repeats the URL's credentials: %v", err)
			}
		})
	}
	for _, key := range []string{"", "has space", "new\nline"} {
		if _, err := jellyfin.New("http://jellyfin:8096", key); err == nil {
			t.Errorf("New accepted key %q", key)
		}
	}
}

func TestPathPrefixIsKept(t *testing.T) {
	srv := jellyfintest.New(t, jellyfintest.WithFixtures())
	mux := http.NewServeMux()
	mux.Handle("/jellyfin/", http.StripPrefix("/jellyfin", srv.Config.Handler))
	proxy := &http.Server{Handler: mux, ReadHeaderTimeout: time.Second}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = proxy.Serve(l) }()
	t.Cleanup(func() { _ = proxy.Close() })
	h := newHarnessFor(t, srv, "http://"+l.Addr().String()+"/jellyfin/", srv.APIKey())
	if _, err := h.c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping through a path prefix: %v", err)
	}
}
