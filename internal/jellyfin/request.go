package jellyfin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// call describes one API request. Only GETs are retried.
type call struct {
	method string
	path   string // already escaped
	query  url.Values
	body   []byte
	image  bool
}

// send performs the call, retrying idempotent GETs on 503 and connection
// errors, and returns the response for a 2xx status. The caller closes the
// body. Any other status becomes a *StatusError.
func (c *Client) send(ctx context.Context, cl call) (*http.Response, error) {
	attempts := 1
	if cl.method == http.MethodGet {
		attempts = c.maxAttempts
	}
	for attempt := 1; ; attempt++ {
		req, err := c.newRequest(ctx, cl)
		if err != nil {
			return nil, err
		}
		resp, err := c.client(cl).Do(req)
		if err != nil && ctx.Err() != nil {
			return nil, fmt.Errorf("jellyfin: %s %s: %w", cl.method, cl.path, ctx.Err())
		}
		wait, retry := c.retryWait(resp, err, attempt)
		if !retry || attempt >= attempts {
			return c.finish(cl, resp, err)
		}
		drain(resp)
		c.log.Warn("jellyfin: retrying request", "method", cl.method, "path", cl.path,
			"attempt", attempt+1, "of", attempts, "wait", wait, "reason", retryReason(resp, err))
		if err := c.sleep(ctx, wait); err != nil {
			return nil, fmt.Errorf("jellyfin: %s %s: %w", cl.method, cl.path, err)
		}
	}
}

func (c *Client) newRequest(ctx context.Context, cl call) (*http.Request, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("jellyfin: %s %s: %w", cl.method, cl.path, err)
	}
	target := c.baseString + cl.path
	if len(cl.query) > 0 {
		target += "?" + cl.query.Encode()
	}
	var body io.Reader
	if cl.body != nil {
		body = bytes.NewReader(cl.body)
	}
	req, err := http.NewRequestWithContext(ctx, cl.method, target, body)
	if err != nil {
		return nil, fmt.Errorf("jellyfin: building %s %s: %w", cl.method, cl.path, err)
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Accept", "application/json")
	if cl.image {
		req.Header.Set("Accept", "image/*")
	}
	if cl.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func (c *Client) client(cl call) *http.Client {
	if cl.image {
		return c.imageHTTP
	}
	return c.http
}

// retryWait decides whether an attempt should be retried and how long to wait.
func (c *Client) retryWait(resp *http.Response, err error, attempt int) (time.Duration, bool) {
	backoff := c.backoff << (attempt - 1)
	switch {
	case err != nil:
		// The caller's context is still live (send checked), so this is a
		// refused connection, a DNS failure or the client timeout.
		return min(backoff, c.maxRetryWait), true
	case resp.StatusCode == http.StatusServiceUnavailable:
		if d, ok := parseRetryAfter(resp.Header.Get("Retry-After"), c.now()); ok {
			return min(d, c.maxRetryWait), true
		}
		return min(backoff, c.maxRetryWait), true
	}
	return 0, false
}

// finish turns the last attempt into a result or a typed error.
func (c *Client) finish(cl call, resp *http.Response, err error) (*http.Response, error) {
	if err != nil {
		return nil, &UnavailableError{BaseURL: c.baseString, Err: err}
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp, nil
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	return nil, &StatusError{
		Method:     cl.method,
		Path:       cl.path,
		StatusCode: resp.StatusCode,
		Body:       c.redact(strings.TrimSpace(string(b))),
	}
}

// getJSON performs a GET and decodes the JSON response into out.
func (c *Client) getJSON(ctx context.Context, path string, q url.Values, out any) error {
	resp, err := c.send(ctx, call{method: http.MethodGet, path: path, query: q})
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("jellyfin: reading GET %s: %w", path, ctxErr)
		}
		return fmt.Errorf("jellyfin: decoding GET %s: %s", path, c.redact(err.Error()))
	}
	return nil
}

// post performs a POST with an optional JSON body and discards the response.
func (c *Client) post(ctx context.Context, path string, q url.Values, body any) error {
	var b []byte
	if body != nil {
		var err error
		if b, err = json.Marshal(body); err != nil {
			return fmt.Errorf("jellyfin: encoding POST %s: %w", path, err)
		}
	}
	resp, err := c.send(ctx, call{method: http.MethodPost, path: path, query: q, body: b})
	if err != nil {
		return err
	}
	drain(resp)
	return nil
}

// redact removes the API key from text that came from the server. Jellyfin
// has no reason to echo it, but an error message must never carry it.
func (c *Client) redact(s string) string {
	if c.apiKey == "" {
		return s
	}
	return strings.ReplaceAll(s, c.apiKey, "[redacted]")
}

func drain(resp *http.Response) {
	if resp == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
}

func retryReason(resp *http.Response, err error) string {
	if err != nil {
		return transportCause(err).Error()
	}
	return "HTTP " + strconv.Itoa(resp.StatusCode)
}

// parseRetryAfter reads a Retry-After header in seconds or as an HTTP date.
func parseRetryAfter(v string, now time.Time) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0, false
		}
		return time.Duration(secs) * time.Second, true
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(t.Sub(now), 0), true
	}
	return 0, false
}

// escape builds a path from literal segments and escaped IDs:
// escape("/Items/%s/Refresh", id).
func escape(format string, ids ...string) string {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = url.PathEscape(id)
	}
	return fmt.Sprintf(format, args...)
}
