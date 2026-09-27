package jellyfin

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// Sentinel errors that callers branch on with errors.Is.
var (
	// ErrUnauthorized means Jellyfin rejected the API key (HTTP 401 or 403).
	ErrUnauthorized = errors.New("jellyfin: the API key was rejected; check the API key in JellyTrim's settings")
	// ErrNotFound means the item, user or route does not exist (HTTP 404).
	ErrNotFound = errors.New("jellyfin: not found")
	// ErrUnavailable means Jellyfin could not be reached (connection refused,
	// DNS failure, timeout) or said it is not ready (HTTP 502, 503, 504).
	ErrUnavailable = errors.New("jellyfin: server unavailable")
	// ErrIncompletePass means the item list kept changing while it was being
	// paged (usually a library scan), so the result may be missing items.
	// Callers must not remove items that were not seen.
	ErrIncompletePass = errors.New("jellyfin: the item list changed during the sync; try again after the library scan finishes")
)

// maxErrorBody caps how much of a response body an error carries.
const maxErrorBody = 512

// StatusError is an unexpected HTTP status from Jellyfin. It matches
// ErrUnauthorized, ErrNotFound or ErrUnavailable with errors.Is where the
// status means that.
type StatusError struct {
	Method string
	// Path is the request path without the query string.
	Path       string
	StatusCode int
	// Body is the start of the response body, with the API key removed.
	Body string
}

// Error describes the failure in terms a user can act on.
func (e *StatusError) Error() string {
	msg := fmt.Sprintf("jellyfin: %s %s: HTTP %d %s", e.Method, e.Path, e.StatusCode, http.StatusText(e.StatusCode))
	switch {
	case e.StatusCode == http.StatusUnauthorized || e.StatusCode == http.StatusForbidden:
		return msg + ": the API key was rejected; check the API key in JellyTrim's settings"
	case e.StatusCode == http.StatusServiceUnavailable:
		return msg + ": Jellyfin is starting up or busy; try again shortly"
	case e.Body != "":
		return msg + ": " + e.Body
	}
	return msg
}

// Is lets errors.Is match the sentinel that fits the status.
func (e *StatusError) Is(target error) bool {
	switch target {
	case ErrUnauthorized:
		return e.StatusCode == http.StatusUnauthorized || e.StatusCode == http.StatusForbidden
	case ErrNotFound:
		return e.StatusCode == http.StatusNotFound
	case ErrUnavailable:
		return e.StatusCode == http.StatusBadGateway ||
			e.StatusCode == http.StatusServiceUnavailable ||
			e.StatusCode == http.StatusGatewayTimeout
	}
	return false
}

// UnavailableError means the request never got an HTTP response: the
// connection was refused, the name did not resolve, or it timed out.
type UnavailableError struct {
	// BaseURL is the configured server URL. It never contains credentials.
	BaseURL string
	Err     error
}

// Error says what to check.
func (e *UnavailableError) Error() string {
	return fmt.Sprintf("jellyfin: JellyTrim could not reach %s. Check the URL and that both containers share a network: %v",
		e.BaseURL, transportCause(e.Err))
}

// Is matches ErrUnavailable.
func (e *UnavailableError) Is(target error) bool { return target == ErrUnavailable }

// Unwrap returns the transport error.
func (e *UnavailableError) Unwrap() error { return e.Err }

// transportCause strips the url.Error wrapper, which repeats the method and
// full request URL, leaving the dial, DNS or timeout detail.
func transportCause(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}
