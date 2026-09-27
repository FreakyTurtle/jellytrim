package jellyfin_test

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/freakyturtle/jellytrim/internal/jellyfin"
	"github.com/freakyturtle/jellytrim/internal/jellyfin/jellyfintest"
)

// sleeper records retry waits instead of sleeping.
type sleeper struct {
	mu    sync.Mutex
	waits []time.Duration
}

func (s *sleeper) sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.waits = append(s.waits, d)
	return nil
}

func (s *sleeper) got() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Duration(nil), s.waits...)
}

// syncBuffer is a log sink safe for the concurrent writes slog may make.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// harness is a client wired to a fake server, with recorded waits and logs.
type harness struct {
	srv   *jellyfintest.Server
	c     *jellyfin.Client
	sleep *sleeper
	logs  *syncBuffer
}

func newHarness(t *testing.T, srvOpts []jellyfintest.Option, opts ...jellyfin.Option) *harness {
	t.Helper()
	srv := jellyfintest.New(t, srvOpts...)
	return newHarnessFor(t, srv, srv.URL, srv.APIKey(), opts...)
}

func newHarnessFor(t *testing.T, srv *jellyfintest.Server, url, key string, opts ...jellyfin.Option) *harness {
	t.Helper()
	h := &harness{srv: srv, sleep: &sleeper{}, logs: &syncBuffer{}}
	base := []jellyfin.Option{
		jellyfin.WithSleep(h.sleep.sleep),
		jellyfin.WithLogger(slog.New(slog.NewTextHandler(h.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))),
		jellyfin.WithDevice("test-host"),
		jellyfin.WithDeviceID("test-device"),
		jellyfin.WithVersion("0.0.0-test"),
	}
	c, err := jellyfin.New(url, key, append(base, opts...)...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.c = c
	return h
}

func fixtures() []jellyfintest.Option { return []jellyfintest.Option{jellyfintest.WithFixtures()} }
