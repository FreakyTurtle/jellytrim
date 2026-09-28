// Package library keeps JellyTrim's picture of the Jellyfin library up to
// date: it syncs items and watch state from Jellyfin, inspects changed files
// with ffprobe, and evaluates every item against the policies.
package library

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/freakyturtle/jellytrim/internal/jellyfin"
	"github.com/freakyturtle/jellytrim/internal/pathmap"
	"github.com/freakyturtle/jellytrim/internal/plan"
	"github.com/freakyturtle/jellytrim/internal/store"
	"github.com/freakyturtle/jellytrim/internal/version"
)

// Prober inspects a file. Implemented by internal/ffmpeg.Runner.
type Prober interface {
	Probe(ctx context.Context, path string) (probeJSON, frameJSON []byte, err error)
}

// Errors callers branch on.
var (
	ErrNotConfigured = errors.New("Jellyfin is not connected yet")
	ErrBusy          = errors.New("a sync is already running")
)

// Status describes what the service is doing, for the UI.
type Status struct {
	Running   bool
	Phase     string // "Reading Jellyfin", "Inspecting files", "Evaluating"
	Done      int
	Total     int
	StartedAt time.Time
	LastError string
}

// Service is the library service. It is safe for concurrent use.
type Service struct {
	store    *store.Store
	log      *slog.Logger
	prober   Prober
	encoders plan.Encoders
	now      func() time.Time
	// newClient builds a Jellyfin client; replaced in tests.
	newClient func(url, key string) (*jellyfin.Client, error)

	base   context.Context // the app's lifetime context, for background runs
	syncMu sync.Mutex      // held for a whole sync, probe or evaluation run
	bg     sync.WaitGroup  // background runs, so shutdown can wait for them
	bgMu   sync.Mutex      // guards closed and bg.Add against Wait
	closed bool            // set by Wait: no background run starts after it
	mu     sync.Mutex      // guards status
	status Status
	// evalPending asks for an evaluation after the current run ends.
	evalPending bool
	// pageSize is how many items evaluation reads and decides together
	// (evalBatch); tests make it small to cover many pages.
	pageSize int
}

// Options configure a Service.
type Options struct {
	Store     *store.Store
	Log       *slog.Logger
	Prober    Prober
	Encoders  plan.Encoders
	Now       func() time.Time
	NewClient func(url, key string) (*jellyfin.Client, error)
}

// New builds a Service.
func New(o Options) *Service {
	s := &Service{store: o.Store, log: o.Log, prober: o.Prober, encoders: o.Encoders, now: o.Now, newClient: o.NewClient,
		pageSize: evalBatch}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.newClient == nil {
		s.newClient = func(url, key string) (*jellyfin.Client, error) {
			return jellyfin.New(url, key, jellyfin.WithVersion(version.Version), jellyfin.WithLogger(s.log))
		}
	}
	return s
}

// Start sets the context background runs use; they stop when it ends.
func (s *Service) Start(ctx context.Context) { s.base = ctx }

// RunAsync starts a full cycle in the background unless one is running.
// It reports whether a run was started.
func (s *Service) RunAsync() bool {
	return s.async(func(ctx context.Context) error { _, err := s.run(ctx); return err })
}

// RunProbeAsync inspects changed files and re-evaluates, in the background.
func (s *Service) RunProbeAsync() bool {
	return s.async(func(ctx context.Context) error {
		if _, _, err := s.probeChanged(ctx); err != nil {
			return err
		}
		_, err := s.evaluate(ctx)
		return err
	})
}

// EvaluateAsync re-evaluates in the background, for example after a policy
// or setting changes.
//
// If another run is in progress, an evaluation is scheduled for when it
// ends, so a change is never lost; it then returns false.
func (s *Service) EvaluateAsync() bool {
	if s.async(func(ctx context.Context) error { _, err := s.evaluate(ctx); return err }) {
		return true
	}
	s.mu.Lock()
	s.evalPending = true
	s.mu.Unlock()
	return false
}

// async starts fn in the background. It refuses once the base context is
// done or Wait has been called, so nothing starts after shutdown began.
func (s *Service) async(fn func(context.Context) error) bool {
	ctx := s.base
	if ctx == nil {
		ctx = context.Background()
	}
	s.bgMu.Lock()
	if s.closed || ctx.Err() != nil {
		s.bgMu.Unlock()
		return false
	}
	s.bg.Add(1)
	s.bgMu.Unlock()
	if !s.begin() {
		s.bg.Done()
		return false
	}
	go func() {
		defer s.bg.Done()
		err := fn(ctx)
		if err != nil && ctx.Err() == nil {
			s.log.Error("library: background run failed", "err", err)
		}
		s.end(err)
	}()
	return true
}

// Wait refuses new background runs and blocks until running ones have
// finished. Call it after the context passed to Start is cancelled, before
// closing the store.
func (s *Service) Wait() {
	s.bgMu.Lock()
	s.closed = true
	s.bgMu.Unlock()
	s.bg.Wait()
}

// Status returns a snapshot of the current activity.
func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (s *Service) setPhase(phase string, done, total int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Phase, s.status.Done, s.status.Total = phase, done, total
}

func (s *Service) begin() bool {
	if !s.syncMu.TryLock() {
		return false
	}
	s.mu.Lock()
	s.status = Status{Running: true, StartedAt: s.now(), LastError: s.status.LastError}
	s.mu.Unlock()
	return true
}

func (s *Service) end(err error) {
	s.mu.Lock()
	s.status.Running = false
	s.status.Phase = ""
	if err != nil {
		s.status.LastError = err.Error()
	} else {
		s.status.LastError = ""
	}
	pending := s.evalPending
	s.evalPending = false
	s.mu.Unlock()
	s.syncMu.Unlock()
	if pending {
		go s.EvaluateAsync()
	}
}

// Client returns a Jellyfin client for the saved connection.
func (s *Service) Client(ctx context.Context) (*jellyfin.Client, error) {
	st, err := s.store.Settings(ctx)
	if err != nil {
		return nil, err
	}
	key, err := s.store.JellyfinAPIKey(ctx)
	if err != nil {
		return nil, err
	}
	if st.JellyfinURL == "" || key == "" {
		return nil, ErrNotConfigured
	}
	return s.newClient(st.JellyfinURL, key)
}

// TestConnection checks a URL and key without saving them.
func (s *Service) TestConnection(ctx context.Context, url, key string) (jellyfin.SystemInfo, error) {
	c, err := s.newClient(url, key)
	if err != nil {
		return jellyfin.SystemInfo{}, err
	}
	return c.Ping(ctx)
}

// SaveConnection tests and stores the Jellyfin URL and key. An empty key
// keeps the saved one.
func (s *Service) SaveConnection(ctx context.Context, url, key string) (jellyfin.SystemInfo, error) {
	if key == "" {
		saved, err := s.store.JellyfinAPIKey(ctx)
		if err != nil {
			return jellyfin.SystemInfo{}, err
		}
		key = saved
	}
	if key == "" {
		return jellyfin.SystemInfo{}, errors.New("enter an API key")
	}
	info, err := s.TestConnection(ctx, url, key)
	if err != nil {
		return info, err
	}
	err = s.store.SetSettings(ctx, map[string]string{
		store.KeyJellyfinURL:    url,
		store.KeyJellyfinAPIKey: key,
		store.KeyJellyfinServer: info.ServerName,
	})
	return info, err
}

// RefreshServerInfo reads libraries and users from Jellyfin into the store.
func (s *Service) RefreshServerInfo(ctx context.Context) error {
	c, err := s.Client(ctx)
	if err != nil {
		return err
	}
	libs, err := c.Libraries(ctx)
	if err != nil {
		return fmt.Errorf("reading libraries: %w", err)
	}
	var rows []store.Library
	for _, l := range libs {
		rows = append(rows, store.Library{ID: l.ItemID, Name: l.Name, CollectionType: l.CollectionType, Locations: l.Locations})
	}
	if err := s.store.ReplaceLibraries(ctx, rows); err != nil {
		return err
	}
	users, err := c.Users(ctx)
	if err != nil {
		return fmt.Errorf("reading users: %w", err)
	}
	var us []store.JellyfinUser
	for _, u := range users {
		us = append(us, store.JellyfinUser{ID: u.ID, Name: u.Name, Disabled: u.Policy.IsDisabled})
	}
	return s.store.ReplaceUsers(ctx, us)
}

// Mapper builds the path mapper from the saved mappings.
func (s *Service) Mapper(ctx context.Context) (*pathmap.Mapper, error) {
	rows, err := s.store.PathMappings(ctx)
	if err != nil {
		return nil, err
	}
	ms := make([]pathmap.Mapping, 0, len(rows))
	for _, r := range rows {
		ms = append(ms, pathmap.Mapping{Jellyfin: r.JellyfinPrefix, Local: r.LocalPrefix})
	}
	return pathmap.New(ms)
}
