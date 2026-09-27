// Package jellyfin is JellyTrim's client for the Jellyfin HTTP API. It reads
// libraries, users, items and watch state, and asks Jellyfin to rescan files
// JellyTrim has replaced. It never changes Jellyfin's settings or metadata.
//
// Every request authenticates with the MediaBrowser Authorization header. The
// API key never appears in a URL, an error or a log line.
package jellyfin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/freakyturtle/jellytrim/internal/version"
)

// Defaults for New.
const (
	DefaultTimeout      = 30 * time.Second
	DefaultImageTimeout = 15 * time.Second
	DefaultMaxAttempts  = 3
	DefaultBackoff      = 500 * time.Millisecond
	// DefaultMaxRetryWait caps how long one Retry-After or backoff wait lasts.
	DefaultMaxRetryWait = 30 * time.Second
	clientName          = "JellyTrim"
)

// Client talks to one Jellyfin server. It is safe for concurrent use.
type Client struct {
	baseString   string
	apiKey       string
	http         *http.Client
	imageHTTP    *http.Client
	device       string
	deviceID     string
	version      string
	auth         string
	maxAttempts  int
	backoff      time.Duration
	maxRetryWait time.Duration
	sleep        func(ctx context.Context, d time.Duration) error
	now          func() time.Time
	log          *slog.Logger
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient sets the HTTP client. Image requests use a copy of it with a
// timeout of at most DefaultImageTimeout.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.http = hc }
}

// WithDevice sets the device name Jellyfin shows for JellyTrim's sessions.
// The default is the host name.
func WithDevice(name string) Option {
	return func(c *Client) { c.device = name }
}

// WithDeviceID sets the device ID. Keep it stable so Jellyfin does not list a
// new device on every restart. The default is derived from the host name.
func WithDeviceID(id string) Option {
	return func(c *Client) { c.deviceID = id }
}

// WithVersion sets the version sent to Jellyfin. The default is the build
// version.
func WithVersion(v string) Option {
	return func(c *Client) { c.version = v }
}

// WithRetry sets how many attempts an idempotent GET gets (including the
// first), the first backoff after a connection error (doubled each time), and
// the longest single wait, which also caps Retry-After.
func WithRetry(maxAttempts int, backoff, maxWait time.Duration) Option {
	return func(c *Client) {
		c.maxAttempts = max(maxAttempts, 1)
		c.backoff = backoff
		c.maxRetryWait = maxWait
	}
}

// WithSleep replaces the wait between retries. Tests use it to record waits
// without sleeping. The function must return ctx.Err() if ctx ends first.
func WithSleep(sleep func(ctx context.Context, d time.Duration) error) Option {
	return func(c *Client) { c.sleep = sleep }
}

// WithClock replaces time.Now, used to read Retry-After dates.
func WithClock(now func() time.Time) Option {
	return func(c *Client) { c.now = now }
}

// WithLogger sets the logger for retry messages. The default is
// slog.Default().
func WithLogger(l *slog.Logger) Option {
	return func(c *Client) { c.log = l }
}

// New returns a client for the server at baseURL, authenticating with apiKey.
// The URL must be http or https, without credentials, a query or a fragment.
func New(baseURL, apiKey string, opts ...Option) (*Client, error) {
	base, err := parseBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	if err := checkAPIKey(apiKey); err != nil {
		return nil, err
	}
	c := &Client{
		baseString:   base.String(),
		apiKey:       apiKey,
		http:         &http.Client{Timeout: DefaultTimeout},
		version:      version.Version,
		maxAttempts:  DefaultMaxAttempts,
		backoff:      DefaultBackoff,
		maxRetryWait: DefaultMaxRetryWait,
		sleep:        sleepContext,
		now:          time.Now,
	}
	for _, o := range opts {
		o(c)
	}
	c.fillDefaults()
	c.auth = authHeader(c.device, c.deviceID, c.version, apiKey)
	return c, nil
}

func (c *Client) fillDefaults() {
	host, _ := os.Hostname()
	if c.device == "" {
		c.device = host
	}
	if c.device == "" {
		c.device = "jellytrim"
	}
	if c.deviceID == "" {
		sum := sha256.Sum256([]byte("jellytrim:" + c.device))
		c.deviceID = "jellytrim-" + hex.EncodeToString(sum[:8])
	}
	if c.log == nil {
		c.log = slog.Default()
	}
	img := *c.http
	if img.Timeout == 0 || img.Timeout > DefaultImageTimeout {
		img.Timeout = DefaultImageTimeout
	}
	c.imageHTTP = &img
}

// BaseURL returns the server URL the client was built with. It never
// contains credentials.
func (c *Client) BaseURL() string { return c.baseString }

// parseBaseURL validates the server URL. Its errors never repeat the URL,
// because a user may have pasted the API key into it.
func parseBaseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, errors.New("jellyfin: the server URL is not a valid URL")
	}
	switch {
	case u.Scheme != "http" && u.Scheme != "https":
		return nil, errors.New("jellyfin: the server URL must start with http:// or https://")
	case u.Host == "":
		return nil, errors.New("jellyfin: the server URL has no host name")
	case u.User != nil:
		return nil, errors.New("jellyfin: the server URL must not contain a user name or password")
	case u.RawQuery != "" || u.ForceQuery:
		return nil, errors.New("jellyfin: the server URL must not contain a query string; enter the API key separately")
	case u.Fragment != "":
		return nil, errors.New("jellyfin: the server URL must not contain a fragment (#)")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u, nil
}

func checkAPIKey(key string) error {
	if key == "" {
		return errors.New("jellyfin: the API key is empty")
	}
	for _, r := range key {
		if r <= ' ' || r == 0x7f {
			return errors.New("jellyfin: the API key contains spaces or control characters")
		}
	}
	return nil
}

// authHeader builds the MediaBrowser Authorization value. Every value is
// quoted, with quotes and backslashes escaped and control characters dropped.
func authHeader(device, deviceID, ver, token string) string {
	return "MediaBrowser Client=" + quote(clientName) +
		", Device=" + quote(device) +
		", DeviceId=" + quote(deviceID) +
		", Version=" + quote(ver) +
		", Token=" + quote(token)
}

func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < ' ' || r == 0x7f:
			// Dropped: a header value cannot carry control characters.
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
