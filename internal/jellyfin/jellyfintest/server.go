// Package jellyfintest is a fake Jellyfin server for tests, built on
// httptest. It serves the endpoints JellyTrim uses with response shapes taken
// from a real Jellyfin 12.1 server, enforces the MediaBrowser Authorization
// header the way Jellyfin 12 does, and can inject faults: error statuses,
// Retry-After, dropped connections, delays and item lists that shift while
// they are being paged.
package jellyfintest

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// Token is the default API key the fake server accepts. It is the fake token
// the public check recognises.
const Token = "deadbeefdeadbeefdeadbeefdeadbeef"

// Default server identity, matching the dev server the fixtures came from.
const (
	DefaultServerName = "jellytrim-dev"
	DefaultVersion    = "12.1.0"
	DefaultServerID   = "3952ac3972a6415bb5d31a97d8e7282c"
)

// Server is a fake Jellyfin server. Its methods are safe for concurrent use.
type Server struct {
	*httptest.Server

	t          testing.TB
	token      string
	serverName string
	version    string
	serverID   string
	legacyOnly bool

	mu          sync.Mutex
	libraries   []Library
	users       []User
	items       map[string]*Item
	userData    map[string]map[string]UserData
	collections []Collection
	faults      map[string][]Fault
	mutations   []mutation
	itemPages   int
	delay       time.Duration
	requests    []Request
	updates     []MediaUpdate
	updateRaw   [][]byte
	refreshes   []Refresh
}

// Request is a request the fake server received.
type Request struct {
	Method        string
	Path          string
	Query         url.Values
	Authorization string
	Header        http.Header
}

// MediaUpdate is one entry of a /Library/Media/Updated call.
type MediaUpdate struct {
	Path       string `json:"Path"`
	UpdateType string `json:"UpdateType"`
}

// Refresh is one /Items/{id}/Refresh call.
type Refresh struct {
	ItemID string
	Query  url.Values
}

// Fault is a response injected in place of the real one.
type Fault struct {
	// Status is the HTTP status to send. Zero with DropConnection unset
	// means 500.
	Status int
	// RetryAfter, when positive, is sent as a Retry-After header in whole
	// seconds.
	RetryAfter time.Duration
	// Body is the response body.
	Body string
	// DropConnection closes the connection without a response, as a
	// crashed or restarting server would.
	DropConnection bool
}

// Option configures a Server.
type Option func(*Server)

// WithToken sets the API key the server accepts.
func WithToken(token string) Option { return func(s *Server) { s.token = token } }

// WithServerName sets the server name reported by /System/Info.
func WithServerName(name string) Option { return func(s *Server) { s.serverName = name } }

// WithVersion sets the version reported by /System/Info.
func WithVersion(v string) Option { return func(s *Server) { s.version = v } }

// WithLegacyItemRoute makes GET /Items/{id} answer 404, as servers before
// 10.9 do, so clients must use /Users/{userId}/Items/{id}.
func WithLegacyItemRoute() Option { return func(s *Server) { s.legacyOnly = true } }

// WithFixtures loads the dataset captured from the Jellyfin 12.1 dev server
// (internal/jellyfin/testdata): two libraries, one user, twelve movies and
// five episodes, with that user's watch state.
func WithFixtures() Option {
	return func(s *Server) {
		if err := s.loadFixtures(); err != nil {
			s.t.Fatalf("jellyfintest: loading fixtures: %v", err)
		}
	}
}

// New starts a fake server that stops when the test ends.
func New(t testing.TB, opts ...Option) *Server {
	t.Helper()
	s := &Server{
		t:          t,
		token:      Token,
		serverName: DefaultServerName,
		version:    DefaultVersion,
		serverID:   DefaultServerID,
		items:      make(map[string]*Item),
		userData:   make(map[string]map[string]UserData),
		faults:     make(map[string][]Fault),
	}
	for _, o := range opts {
		o(s)
	}
	s.Server = httptest.NewServer(s.handler())
	t.Cleanup(s.Close)
	return s
}

// APIKey returns the key the server accepts.
func (s *Server) APIKey() string { return s.token }

// AddLibrary adds a library.
func (s *Server) AddLibrary(l Library) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.libraries = append(s.libraries, l)
}

// AddUser adds a user.
func (s *Server) AddUser(u User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users = append(s.users, u)
}

// AddItem adds or replaces an item. Its library is found from its path
// when LibraryID is empty.
func (s *Server) AddItem(it Item) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addItemLocked(it)
}

func (s *Server) addItemLocked(it Item) {
	fillItemDefaults(&it)
	if it.LibraryID == "" {
		it.LibraryID = libraryFor(s.libraries, it.Path)
	}
	if it.Image != nil && it.ImageTags["Primary"] == "" {
		if it.ImageTags == nil {
			it.ImageTags = map[string]string{}
		}
		it.ImageTags["Primary"] = "tag" + it.ID
	}
	s.items[it.ID] = &it
}

// RemoveItem removes an item, as a library scan would after a file is
// deleted.
func (s *Server) RemoveItem(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, id)
}

// SetUserData sets one user's watch state for one item.
func (s *Server) SetUserData(userID, itemID string, ud UserData) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setUserDataLocked(userID, itemID, ud)
}

func (s *Server) setUserDataLocked(userID, itemID string, ud UserData) {
	userID = normaliseID(userID)
	if s.userData[userID] == nil {
		s.userData[userID] = make(map[string]UserData)
	}
	s.userData[userID][itemID] = ud
}

// AddCollection adds a collection (box set).
func (s *Server) AddCollection(c Collection) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.collections = append(s.collections, c)
}

// FailNext makes the next request for path (for example "/Items") fail with
// status, and a Retry-After header when retryAfter is positive. Calls queue:
// two FailNext calls fail the next two requests. A status of 0 drops the
// connection instead.
func (s *Server) FailNext(path string, status int, retryAfter time.Duration) {
	s.Inject(path, Fault{Status: status, RetryAfter: retryAfter, DropConnection: status == 0})
}

// Inject queues a fault for the next request for path.
func (s *Server) Inject(path string, f Fault) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.faults[path] = append(s.faults[path], f)
}

// mutation changes the dataset once a number of /Items pages have been
// served, to simulate a library scan running during a sync.
type mutation struct {
	afterPages int
	apply      func(*Server)
}

// ShiftPaging inserts it into the dataset after afterPages /Items list
// responses have been served. An item that sorts before the pages already
// read pushes every later item down by one, so the next page repeats the
// last item of the previous one.
func (s *Server) ShiftPaging(afterPages int, it Item) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mutations = append(s.mutations, mutation{afterPages, func(s *Server) { s.addItemLocked(it) }})
}

// RemoveDuringPaging removes the item with id after afterPages /Items list
// responses have been served, pulling later items up by one so a naive
// pager skips one.
func (s *Server) RemoveDuringPaging(afterPages int, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mutations = append(s.mutations, mutation{afterPages, func(s *Server) { delete(s.items, id) }})
}

// Delay makes every later request wait d before it is answered (or until
// the client gives up).
func (s *Server) Delay(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delay = d
}

// Requests returns every request received so far.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

// RequestsFor returns the requests received for one path.
func (s *Server) RequestsFor(path string) []Request {
	var out []Request
	for _, r := range s.Requests() {
		if r.Path == path {
			out = append(out, r)
		}
	}
	return out
}

// MediaUpdates returns every path passed to /Library/Media/Updated.
func (s *Server) MediaUpdates() []MediaUpdate {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]MediaUpdate(nil), s.updates...)
}

// MediaUpdateBodies returns the raw bodies of /Library/Media/Updated calls.
func (s *Server) MediaUpdateBodies() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]byte(nil), s.updateRaw...)
}

// Refreshes returns every /Items/{id}/Refresh call.
func (s *Server) Refreshes() []Refresh {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Refresh(nil), s.refreshes...)
}

// AuthHeaderFields parses a MediaBrowser Authorization value into its
// fields. It returns false unless the value has the MediaBrowser scheme and
// every field is a quoted string.
func AuthHeaderFields(h string) (map[string]string, bool) {
	rest, ok := strings.CutPrefix(h, "MediaBrowser ")
	if !ok {
		return nil, false
	}
	fields := make(map[string]string)
	for rest != "" {
		key, after, ok := strings.Cut(rest, "=")
		if !ok || len(after) == 0 || after[0] != '"' {
			return nil, false
		}
		val, remain, ok := readQuoted(after[1:])
		if !ok {
			return nil, false
		}
		fields[strings.TrimSpace(key)] = val
		rest = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(remain), ","))
	}
	return fields, true
}

// readQuoted reads up to the closing quote, undoing backslash escapes.
func readQuoted(s string) (val, rest string, ok bool) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			if i+1 >= len(s) {
				return "", "", false
			}
			i++
			b.WriteByte(s[i])
		case '"':
			return b.String(), s[i+1:], true
		default:
			b.WriteByte(s[i])
		}
	}
	return "", "", false
}

func (s *Server) String() string {
	return fmt.Sprintf("jellyfintest.Server(%s)", s.URL)
}
