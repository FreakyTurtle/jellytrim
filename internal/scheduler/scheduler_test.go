package scheduler

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
	_ "time/tzdata" // Europe/London in environments without zoneinfo

	"github.com/freakyturtle/jellytrim/internal/library"
	"github.com/freakyturtle/jellytrim/internal/store"
)

func TestSyncDue(t *testing.T) {
	now := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	at := func(h, m int, dayOffset int) *time.Time {
		v := time.Date(2026, 9, 27+dayOffset, h, m, 0, 0, time.UTC)
		return &v
	}
	cases := []struct {
		name     string
		last     *time.Time
		interval int
		daily    string
		want     bool
	}{
		{"first sync", nil, 6, "", true},
		{"interval not reached", at(5, 0, 0), 6, "", false},
		{"interval reached", at(4, 0, 0), 6, "", true},
		{"zero interval means default", at(3, 59, 0), 0, "", true},
		{"daily time passed, not synced since", at(2, 0, 0), 6, "03:00", true},
		{"daily time passed, synced after it", at(3, 30, 0), 6, "03:00", false},
		{"daily time not yet reached, yesterday's slot missed", at(3, 30, -1), 6, "23:00", true},
		{"daily time not yet reached, yesterday's slot done", at(23, 5, -1), 6, "23:00", false},
		{"daily time exactly now", at(9, 0, 0), 6, "10:00", true},
		{"bad daily time falls back to interval", at(9, 0, 0), 6, "25:00", false},
	}
	for _, tc := range cases {
		if got := SyncDue(now, tc.last, tc.interval, tc.daily); got != tc.want {
			t.Errorf("%s: got %v", tc.name, got)
		}
	}
}

func TestSyncDueDailyInLondonAcrossDST(t *testing.T) {
	london, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Fatal(err)
	}
	local := func(y int, m time.Month, d, h, min int) time.Time { return time.Date(y, m, d, h, min, 0, 0, london) }
	ptr := func(v time.Time) *time.Time { return &v }
	cases := []struct {
		name string
		now  time.Time
		last time.Time
		want bool
	}{
		// Clocks go forward at 01:00 GMT on 29 March 2026.
		{"spring: slot passed on the change day", local(2026, 3, 29, 3, 30), local(2026, 3, 28, 3, 5), true},
		{"spring: synced at the slot on the change day", local(2026, 3, 29, 9, 0), local(2026, 3, 29, 3, 1), false},
		{"spring: before the slot, yesterday's slot done", local(2026, 3, 29, 2, 30), local(2026, 3, 28, 3, 0), false},
		// 03:00 BST is 02:00 UTC: a sync at 02:30 UTC is after the slot.
		{"spring: a UTC time after the local slot counts", local(2026, 3, 29, 12, 0),
			time.Date(2026, 3, 29, 2, 30, 0, 0, time.UTC), false},
		// Clocks go back at 02:00 BST on 25 October 2026.
		{"autumn: slot passed on the change day", local(2026, 10, 25, 3, 30), local(2026, 10, 24, 3, 5), true},
		{"autumn: synced at the slot on the change day", local(2026, 10, 25, 18, 0), local(2026, 10, 25, 3, 0), false},
		{"autumn: before the slot, yesterday's slot missed", local(2026, 10, 25, 1, 30), local(2026, 10, 23, 3, 0), true},
	}
	for _, tc := range cases {
		if got := SyncDue(tc.now, ptr(tc.last), 6, "03:00"); got != tc.want {
			t.Errorf("%s: got %v", tc.name, got)
		}
	}
}

type fakeStore struct {
	settings    store.Settings
	settingsErr error
	last        store.SyncRun
	lastErr     error
	complete    store.SyncRun
	completeErr error
	legacyLeft  int // uncompressed probe rows still to do
	legacyCalls int
}

func (f *fakeStore) Settings(context.Context) (store.Settings, error) {
	return f.settings, f.settingsErr
}
func (f *fakeStore) LastSync(context.Context) (store.SyncRun, error) { return f.last, f.lastErr }

func (f *fakeStore) CompressLegacyProbes(_ context.Context, limit int) (int, error) {
	f.legacyCalls++
	n := min(limit, f.legacyLeft)
	f.legacyLeft -= n
	return n, nil
}
func (f *fakeStore) LastCompleteSync(context.Context) (store.SyncRun, error) {
	return f.complete, f.completeErr
}

type fakeLibrary struct {
	busy    bool
	running bool
	started int
}

func (f *fakeLibrary) RunAsync() bool {
	if f.busy {
		return false
	}
	f.started++
	return true
}
func (f *fakeLibrary) Status() library.Status { return library.Status{Running: f.running} }

type fakeQueue struct{ enqueued, expired int }

func (f *fakeQueue) EnqueueMatching(context.Context) (int, error) { f.enqueued++; return 0, nil }
func (f *fakeQueue) ExpireBackups(context.Context)                { f.expired++ }

type rig struct {
	s   *Scheduler
	st  *fakeStore
	lib *fakeLibrary
	q   *fakeQueue
	now time.Time
}

func newRig() *rig {
	r := &rig{
		st:  &fakeStore{settings: store.Settings{SetupComplete: true, SyncIntervalHours: 6}},
		lib: &fakeLibrary{},
		q:   &fakeQueue{},
		now: time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC),
	}
	r.s = &Scheduler{Store: r.st, Library: r.lib, Queue: r.q, Now: func() time.Time { return r.now },
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	return r
}

func (r *rig) step() { r.s.step(context.Background()) }

func (r *rig) synced(id int64, ago time.Duration, status string) {
	r.st.last = store.SyncRun{ID: id, StartedAt: r.now.Add(-ago), Status: status}
	r.st.lastErr = nil
	if status == "complete" {
		r.st.complete = r.st.last
		r.st.completeErr = nil
	}
}

func TestStepStartsTheFirstSync(t *testing.T) {
	r := newRig()
	r.st.lastErr, r.st.completeErr = store.ErrNotFound, store.ErrNotFound
	r.step()
	if r.lib.started != 1 || r.q.enqueued != 0 {
		t.Fatalf("started %d, enqueued %d", r.lib.started, r.q.enqueued)
	}
}

func TestStepDoesNothingBeforeSetup(t *testing.T) {
	r := newRig()
	r.st.settings.SetupComplete = false
	r.st.lastErr = store.ErrNotFound
	r.step()
	if r.lib.started != 0 || r.q.enqueued != 0 || r.q.expired != 0 {
		t.Fatalf("%+v %+v", r.lib, r.q)
	}
	r.st.settingsErr = errors.New("database is locked")
	r.st.settings.SetupComplete = true
	r.step()
	if r.lib.started != 0 {
		t.Fatal("a sync started without readable settings")
	}
}

func TestStepEnqueuesOncePerCompleteSyncThenEveryTenMinutes(t *testing.T) {
	r := newRig()
	r.synced(1, time.Hour, "complete")
	r.step()
	if r.lib.started != 0 || r.q.enqueued != 1 || r.q.expired != 1 {
		t.Fatalf("after a complete sync: started %d, enqueued %d, expired %d", r.lib.started, r.q.enqueued, r.q.expired)
	}
	r.now = r.now.Add(time.Minute)
	r.step()
	if r.q.enqueued != 1 {
		t.Fatalf("enqueued again after a minute: %d", r.q.enqueued)
	}
	r.now = r.now.Add(10 * time.Minute)
	r.step()
	if r.q.enqueued != 2 {
		t.Fatalf("not enqueued after ten minutes: %d", r.q.enqueued)
	}
	r.synced(2, 0, "complete") // a new sync finished just now
	r.now = r.now.Add(time.Minute)
	r.step()
	if r.q.enqueued != 3 {
		t.Fatalf("not enqueued after a new complete sync: %d", r.q.enqueued)
	}
}

func TestStepDoesNotEnqueueWhileASyncRuns(t *testing.T) {
	r := newRig()
	r.synced(1, time.Hour, "complete")
	r.lib.running = true
	r.step()
	if r.q.enqueued != 0 {
		t.Fatalf("enqueued during a sync: %d", r.q.enqueued)
	}
}

func TestStepBusyLibraryFallsThroughToQueueing(t *testing.T) {
	r := newRig()
	r.synced(1, 7*time.Hour, "complete") // a sync is due
	r.lib.busy = true                    // but a manual one holds the lock
	r.step()
	if r.lib.started != 0 || r.q.enqueued != 1 {
		t.Fatalf("started %d, enqueued %d", r.lib.started, r.q.enqueued)
	}
	// The scheduler did not start that sync, so it tries again next tick.
	r.lib.busy = false
	r.now = r.now.Add(time.Minute)
	r.step()
	if r.lib.started != 1 {
		t.Fatalf("started %d after the library was free", r.lib.started)
	}
}

func TestStepFailedSyncBacksOffAndQueuesFromLastComplete(t *testing.T) {
	r := newRig()
	r.synced(1, 20*time.Hour, "complete")
	r.synced(2, 30*time.Minute, "failed") // Jellyfin was down half an hour ago
	r.step()
	if r.lib.started != 0 {
		t.Fatal("a failed sync half an hour ago must not be retried before the interval")
	}
	if r.q.enqueued != 1 {
		t.Fatalf("work from the last complete sync was not queued: %d", r.q.enqueued)
	}
	r.now = r.now.Add(6 * time.Hour)
	r.step()
	if r.lib.started != 1 {
		t.Fatal("the next sync did not start at the interval")
	}
}

func TestStepRemembersItsOwnAttempts(t *testing.T) {
	r := newRig()
	r.st.lastErr = errors.New("database disk image is malformed")
	r.st.completeErr = r.st.lastErr
	r.step()
	if r.lib.started != 1 {
		t.Fatalf("first attempt: started %d", r.lib.started)
	}
	for range 30 {
		r.now = r.now.Add(time.Minute)
		r.step()
	}
	if r.lib.started != 1 {
		t.Fatalf("unreadable history restarted the sync on every tick: %d starts", r.lib.started)
	}
	r.now = r.now.Add(6 * time.Hour)
	r.step()
	if r.lib.started != 2 {
		t.Fatalf("no sync after the interval: %d starts", r.lib.started)
	}
}

func TestStepCompressesOldProbesWhenIdle(t *testing.T) {
	r := newRig()
	r.synced(1, time.Hour, "complete")
	r.st.legacyLeft = 2500
	r.lib.running = true
	r.step()
	if r.st.legacyCalls != 0 {
		t.Fatal("compressed while a sync was running")
	}
	r.lib.running = false
	for i := 0; i < 6; i++ {
		r.step()
	}
	if r.st.legacyLeft != 0 || r.st.legacyCalls != 4 {
		t.Fatalf("left %d after %d calls; it should stop once a batch finds nothing", r.st.legacyLeft, r.st.legacyCalls)
	}
}
