// Package scheduler runs JellyTrim's periodic work: syncing with Jellyfin on
// the configured interval or daily time, queueing what the policies chose
// once a sync has finished, and deleting expired backups.
package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/freakyturtle/jellytrim/internal/library"
	"github.com/freakyturtle/jellytrim/internal/store"
)

// Store is the state the scheduler reads. Implemented by *store.Store.
type Store interface {
	Settings(ctx context.Context) (store.Settings, error)
	LastSync(ctx context.Context) (store.SyncRun, error)
	LastCompleteSync(ctx context.Context) (store.SyncRun, error)
	// CompressLegacyProbes compresses probe rows written by older versions.
	CompressLegacyProbes(ctx context.Context, limit int) (int, error)
}

// legacyBatch is how many old uncompressed probe rows are compressed per
// idle tick: about 150 ms of work, so 100,000 rows take under two hours.
const legacyBatch = 1000

// Library starts syncs. Implemented by *library.Service.
type Library interface {
	RunAsync() bool
	Status() library.Status
}

// Queue queues work and expires backups. Implemented by *queue.Service.
type Queue interface {
	EnqueueMatching(ctx context.Context) (int, error)
	ExpireBackups(ctx context.Context)
}

// enqueueEvery re-queues matching items this often even without a new
// sync, in case Dry Run was just turned off or a policy changed.
const enqueueEvery = 10 * time.Minute

// Scheduler ticks once a minute. It never hammers Jellyfin: at most one sync
// runs at a time, and only when one is due.
type Scheduler struct {
	Store   Store
	Library Library
	Queue   Queue
	Log     *slog.Logger
	Now     func() time.Time
	Tick    time.Duration

	// lastAttempt is when this scheduler last started a sync. It stands in
	// for the stored sync history if that cannot be read or written, so a
	// broken database does not start a sync on every tick.
	lastAttempt    time.Time
	lastQueuedSync int64
	// legacyDone is set once no uncompressed probe rows remain.
	legacyDone  bool
	lastExpiry  time.Time
	lastEnqueue time.Time
}

// Run blocks until ctx ends.
func (s *Scheduler) Run(ctx context.Context) {
	if s.Now == nil {
		s.Now = time.Now
	}
	if s.Log == nil {
		s.Log = slog.Default()
	}
	tick := s.Tick
	if tick <= 0 {
		tick = time.Minute
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		s.step(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Scheduler) step(ctx context.Context) {
	st, err := s.Store.Settings(ctx)
	if err != nil {
		s.Log.Warn("scheduler: reading settings", "err", err)
		return
	}
	if !st.SetupComplete {
		return
	}
	now := s.Now()
	if SyncDue(now, s.lastStart(ctx), st.SyncIntervalHours, st.SyncDailyAt) && s.Library.RunAsync() {
		s.lastAttempt = now
		s.Log.Info("scheduler: sync started")
		return
	}
	s.maybeEnqueue(ctx, now)
	if now.Sub(s.lastExpiry) > time.Hour {
		s.lastExpiry = now
		s.Queue.ExpireBackups(ctx)
	}
	if !s.legacyDone && !s.Library.Status().Running {
		n, err := s.Store.CompressLegacyProbes(ctx, legacyBatch)
		switch {
		case err != nil:
			s.Log.Warn("scheduler: compressing old probe data", "err", err)
		case n == 0:
			s.legacyDone = true
		}
	}
}

// lastStart is when the last sync attempt started: the later of the stored
// history (every attempt is recorded, failed or not) and this scheduler's
// own memory. Nil means no sync has ever been attempted.
func (s *Scheduler) lastStart(ctx context.Context) *time.Time {
	var last *time.Time
	run, err := s.Store.LastSync(ctx)
	switch {
	case err == nil:
		last = &run.StartedAt
	case !errors.Is(err, store.ErrNotFound):
		s.Log.Warn("scheduler: reading the last sync", "err", err)
	}
	if !s.lastAttempt.IsZero() && (last == nil || s.lastAttempt.After(*last)) {
		last = &s.lastAttempt
	}
	return last
}

// maybeEnqueue queues work after each new complete sync, and every
// enqueueEvery, based on the last complete sync: a failed sync since then
// does not stop work the last good picture of the library chose.
func (s *Scheduler) maybeEnqueue(ctx context.Context, now time.Time) {
	complete, err := s.Store.LastCompleteSync(ctx)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.Log.Warn("scheduler: reading the last complete sync", "err", err)
		}
		return
	}
	if s.Library.Status().Running {
		return
	}
	if complete.ID == s.lastQueuedSync && now.Sub(s.lastEnqueue) <= enqueueEvery {
		return
	}
	s.lastQueuedSync, s.lastEnqueue = complete.ID, now
	if _, err := s.Queue.EnqueueMatching(ctx); err != nil {
		s.Log.Warn("scheduler: queueing", "err", err)
	}
}

// SyncDue reports whether a sync should start now. With a daily time
// ("HH:MM", in now's location), a sync is due when none has started since
// the most recent slot: today's if it has passed, otherwise yesterday's, so
// a slot missed while JellyTrim was stopped is caught up. Otherwise one runs
// every intervalHours. A first sync is always due.
func SyncDue(now time.Time, lastStart *time.Time, intervalHours int, dailyAt string) bool {
	if lastStart == nil {
		return true
	}
	if mins, ok := parseClock(dailyAt); ok {
		return lastStart.Before(lastSlot(now, mins))
	}
	if intervalHours <= 0 {
		intervalHours = 6
	}
	return now.Sub(*lastStart) >= time.Duration(intervalHours)*time.Hour
}

// lastSlot is the most recent daily slot at or before now. time.Date works
// on wall-clock fields, so the slot stays at HH:MM local across DST changes.
func lastSlot(now time.Time, mins int) time.Time {
	y, m, d := now.Date()
	slot := time.Date(y, m, d, mins/60, mins%60, 0, 0, now.Location())
	if now.Before(slot) {
		slot = time.Date(y, m, d-1, mins/60, mins%60, 0, 0, now.Location())
	}
	return slot
}

func parseClock(v string) (int, bool) {
	if len(v) != 5 || v[2] != ':' {
		return 0, false
	}
	h := int(v[0]-'0')*10 + int(v[1]-'0')
	m := int(v[3]-'0')*10 + int(v[4]-'0')
	if v[0] < '0' || v[0] > '2' || v[1] < '0' || v[1] > '9' || v[3] < '0' || v[3] > '5' || v[4] < '0' || v[4] > '9' || h > 23 {
		return 0, false
	}
	return h*60 + m, true
}
