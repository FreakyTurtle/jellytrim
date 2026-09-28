package queue

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/freakyturtle/jellytrim/internal/store"
	"github.com/freakyturtle/jellytrim/internal/timetable"
)

// These tests need no ffmpeg: they use a real store and a fake filesystem
// for free space.

var testNow = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

// fakeSpace answers by the longest matching folder prefix.
type fakeSpace struct {
	free map[string]uint64
	dev  map[string]uint64
}

func longest[V any](m map[string]V, dir string) (V, bool) {
	var best string
	var v V
	found := false
	for prefix, val := range m {
		if strings.HasPrefix(dir+"/", prefix+"/") && len(prefix) >= len(best) {
			best, v, found = prefix, val, true
		}
	}
	return v, found
}

func (f fakeSpace) Free(dir string) (uint64, error) {
	if v, ok := longest(f.free, dir); ok {
		return v, nil
	}
	return 0, errors.New("no such filesystem")
}

func (f fakeSpace) Device(dir string) (uint64, error) {
	if v, ok := longest(f.dev, dir); ok {
		return v, nil
	}
	return 0, errors.New("no such filesystem")
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st.SetClock(func() time.Time { return testNow })
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func newTestQueue(st *store.Store, space SpaceFS) *Service {
	return New(Options{Store: st, Space: space, Now: func() time.Time { return testNow },
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
}

func ptr(v int64) *int64 { return &v }

const gb = int64(1) << 30

// seedCandidates stores two libraries, items, a policy, probes and
// evaluations, as a sync and evaluation would leave them.
func seedCandidates(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := context.Background()
	must(t, st.ReplaceLibraries(ctx, []store.Library{{ID: "lm", Name: "Movies"}, {ID: "lt", Name: "TV"}, {ID: "lx", Name: "Other"}}))
	must(t, st.SetManagedLibraries(ctx, []string{"lm", "lt"}))
	sync, err := st.StartSync(ctx)
	must(t, err)
	must(t, st.UpsertItems(ctx, sync, []store.Item{
		{ID: "film", LibraryID: "lm", Type: "Movie", Name: "Film", SortName: "film", JellyfinPath: "/jf/film.mkv", LocalPath: "/a/film/film.mkv"},
		{ID: "ep", LibraryID: "lt", Type: "Episode", Name: "Pilot", SortName: "pilot", SeriesName: "Show", JellyfinPath: "/jf/ep.mkv", LocalPath: "/b/show/ep.mkv"},
		{ID: "recent", LibraryID: "lm", Type: "Movie", Name: "Recent", SortName: "recent", JellyfinPath: "/jf/recent.mkv", LocalPath: "/a/recent/recent.mkv"},
		{ID: "ok", LibraryID: "lm", Type: "Movie", Name: "Fine", SortName: "fine", JellyfinPath: "/jf/ok.mkv", LocalPath: "/a/ok/ok.mkv"},
		{ID: "unmanaged", LibraryID: "lx", Type: "Movie", Name: "Elsewhere", SortName: "elsewhere", JellyfinPath: "/jf/x.mkv", LocalPath: "/c/x.mkv"},
	}))
	pid, err := st.SavePolicy(ctx, store.PolicyRow{Name: "Shrink old 4K", Enabled: true, Scope: "{}", Conditions: "[]", Action: "{}"})
	must(t, err)
	must(t, st.SaveProbe(ctx, store.Probe{ItemID: "film", LocalPath: "/a/film/film.mkv", DurationMs: 7_200_000}))
	planJSON := func(size int64) string {
		return fmt.Sprintf(`{"encoder":"x265","source_size":%d,"source_label":"2160p H.264","target_label":"1080p HEVC"}`, size)
	}
	opt := func(id string, size, lo, hi int64) store.Evaluation {
		return store.Evaluation{ItemID: id, Outcome: "optimise", PolicyID: &pid, Plan: planJSON(size), EstMin: ptr(lo), EstMax: ptr(hi)}
	}
	must(t, st.ReplaceEvaluations(ctx, []store.Evaluation{
		opt("film", 20*gb, 4*gb, 6*gb), opt("ep", 2*gb, gb/2, gb), opt("recent", 5*gb, gb, 2*gb),
		{ItemID: "ok", Outcome: "optimal"}, opt("unmanaged", 9*gb, gb, 2*gb),
	}))
}

func TestEnqueueMatchingBuildsJobsInBulk(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	seedCandidates(t, st)
	must(t, st.SetSetting(ctx, store.KeyDryRun, "false"))
	// A job last week holds its item back from automatic queueing.
	old, _, err := st.CreateJob(ctx, store.Job{ItemID: "recent", ItemName: "Recent", Plan: "{}", LocalPath: "/a/recent/recent.mkv"})
	must(t, err)
	must(t, st.FinishJob(ctx, old, store.JobOutcome{Status: store.JobFailed}))
	q := newTestQueue(st, fakeSpace{})

	n, err := q.EnqueueMatching(ctx)
	if err != nil || n != 2 {
		t.Fatalf("queued %d (%v), want 2", n, err)
	}
	jobs, err := st.ActiveJobs(ctx)
	must(t, err)
	byItem := map[string]store.Job{}
	for _, j := range jobs {
		byItem[j.ItemID] = j
	}
	film, ep := byItem["film"], byItem["ep"]
	if film.ItemName != "Film" || film.LibraryName != "Movies" || film.PolicyName != "Shrink old 4K" ||
		film.DurationMs != 7_200_000 || film.EstSaving != 14*gb || *film.SourceSize != 20*gb ||
		film.SourceSummary != "2160p H.264" || film.TargetSummary != "1080p HEVC" || film.LocalPath != "/a/film/film.mkv" ||
		film.JellyfinPath != "/jf/film.mkv" || film.Trigger != "auto" {
		t.Errorf("film job %+v", film)
	}
	if ep.ItemName != "Show · Pilot" || ep.LibraryName != "TV" || ep.DurationMs != 0 {
		t.Errorf("episode job %+v", ep)
	}
	for _, id := range []string{"recent", "ok", "unmanaged"} {
		if _, ok := byItem[id]; ok {
			t.Errorf("queued %s", id)
		}
	}
	// A manual request still queues the recent item, and refuses one the
	// policies did not choose.
	if _, created, err := q.Enqueue(ctx, "recent", "manual"); err != nil || !created {
		t.Fatalf("manual enqueue: %v %v", created, err)
	}
	if _, _, err := q.Enqueue(ctx, "ok", "manual"); !errors.Is(err, ErrNotOptimise) {
		t.Fatalf("enqueue of an optimal item: %v", err)
	}
	if next, _ := st.NextWaitingJob(ctx); next.ItemID != "recent" {
		t.Fatalf("manual job is not first: %s", next.ItemID)
	}
}

// week42 is six active hours every day: 42 hours a week.
func week42() timetable.Week {
	var w timetable.Week
	for d := range w {
		for h := 0; h < 6; h++ {
			w[d][h] = true
		}
	}
	return w
}

func TestBacklog(t *testing.T) {
	ctx := context.Background()
	hours := func(h float64) time.Duration { return time.Duration(h * float64(time.Hour)) }
	cases := []struct {
		name        string
		speeds      []float64 // diagnostics speeds of complete jobs
		concurrency string
		schedule    string
		wantSpeed   float64
		wantProcess time.Duration
		wantCal     time.Duration
		wantHours   int
	}{
		{"no history uses 1x for software and 3x for hardware", nil, "1", week42().String(),
			0, hours(2 + 3.0/3), hours(3 * 4), 42},
		{"history uses the median speed", []float64{1, 4, 2}, "1", week42().String(),
			2, hours(5.0 / 2), hours(2.5 * 4), 42},
		{"two jobs at once halve the time", []float64{2.5, 2.5}, "2", timetable.Always().String(),
			2.5, hours(5.0 / 2.5 / 2), hours(1), 168},
		{"no active hours leaves the calendar time unknown", nil, "1", timetable.Week{}.String(),
			0, hours(3), 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := openStore(t)
			must(t, st.SetSettings(ctx, map[string]string{store.KeyConcurrency: tc.concurrency, store.KeyProcessingSchedule: tc.schedule}))
			for i, sp := range tc.speeds {
				id, _, err := st.CreateJob(ctx, store.Job{ItemID: fmt.Sprintf("done%d", i), ItemName: "x", Plan: "{}"})
				must(t, err)
				must(t, st.FinishJob(ctx, id, store.JobOutcome{Status: store.JobComplete, Diagnostics: fmt.Sprintf(`{"speed":%g}`, sp)}))
			}
			// Failed jobs and jobs without a speed do not count.
			failed, _, _ := st.CreateJob(ctx, store.Job{ItemID: "failed", ItemName: "x", Plan: "{}"})
			must(t, st.FinishJob(ctx, failed, store.JobOutcome{Status: store.JobFailed, Diagnostics: `{"speed":50}`}))
			nospeed, _, _ := st.CreateJob(ctx, store.Job{ItemID: "nospeed", ItemName: "x", Plan: "{}"})
			must(t, st.FinishJob(ctx, nospeed, store.JobOutcome{Status: store.JobComplete, Diagnostics: `{"step":"done"}`}))

			_, err := st.CreateJobs(ctx, []store.Job{
				{ItemID: "sw", ItemName: "x", Plan: `{"encoder":"x265"}`, SourceSize: ptr(10 * gb), EstMin: ptr(2 * gb), EstMax: ptr(4 * gb), DurationMs: 2 * 3_600_000},
				{ItemID: "hw", ItemName: "x", Plan: `{"encoder":"qsv-hevc"}`, SourceSize: ptr(6 * gb), EstMin: ptr(gb), EstMax: ptr(7 * gb), DurationMs: 3 * 3_600_000},
				{ItemID: "unknown", ItemName: "x", Plan: `{"encoder":"x265"}`, SourceSize: ptr(gb), EstMin: ptr(gb / 2), EstMax: ptr(gb)},
			})
			must(t, err)
			b, err := newTestQueue(st, fakeSpace{}).Backlog(ctx)
			must(t, err)
			if b.Jobs != 3 || b.SourceBytes != 17*gb || b.UnknownDuration != 1 {
				t.Errorf("jobs %d, bytes %d, unknown %d", b.Jobs, b.SourceBytes, b.UnknownDuration)
			}
			// Saving: 10-4 + 0 (estimate above the source) + 1-1; up to 10-2 + 6-1 + 1-0.5.
			if b.SavingMin != 6*gb || b.SavingMax != 13*gb+gb/2 {
				t.Errorf("saving %d..%d", b.SavingMin, b.SavingMax)
			}
			if b.Speed != tc.wantSpeed || b.SpeedSamples != len(tc.speeds) {
				t.Errorf("speed %g from %d samples, want %g", b.Speed, b.SpeedSamples, tc.wantSpeed)
			}
			if b.ProcessingTime != tc.wantProcess || b.CalendarTime != tc.wantCal || b.ActiveHoursPerWeek != tc.wantHours {
				t.Errorf("processing %s, calendar %s at %d hours, want %s, %s at %d",
					b.ProcessingTime, b.CalendarTime, b.ActiveHoursPerWeek, tc.wantProcess, tc.wantCal, tc.wantHours)
			}
		})
	}
}

func TestSpaceOutlook(t *testing.T) {
	ctx := context.Background()
	space := fakeSpace{
		free: map[string]uint64{"/a": uint64(10 * gb), "/b": uint64(100 * gb)},
		dev:  map[string]uint64{"/a": 1, "/b": 2},
	}
	cases := []struct {
		name       string
		backupDays string
		wantPeakA  int64
		wantRiskA  bool
	}{
		// 4 + 6 + 1 GB of new files while the originals are kept, on 10 GB free.
		{"backups kept: every output counts", "7", 11 * gb, true},
		{"no backups: the largest output counts", "0", 6 * gb, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := openStore(t)
			must(t, st.SetSetting(ctx, store.KeyBackupDays, tc.backupDays))
			_, err := st.CreateJobs(ctx, []store.Job{
				{ItemID: "a1", ItemName: "x", Plan: "{}", LocalPath: "/a/movies/One/one.mkv", EstMax: ptr(4 * gb)},
				{ItemID: "a2", ItemName: "x", Plan: "{}", LocalPath: "/a/movies/Two/two.mkv", EstMax: ptr(6 * gb)},
				{ItemID: "a3", ItemName: "x", Plan: "{}", LocalPath: "/a/tv/Show/ep.mkv", EstMax: ptr(gb)},
				{ItemID: "b1", ItemName: "x", Plan: "{}", LocalPath: "/b/movies/Three/three.mkv", EstMax: ptr(9 * gb)},
				{ItemID: "gone", ItemName: "x", Plan: "{}", LocalPath: "/missing/x.mkv", EstMax: ptr(gb)},
			})
			must(t, err)
			done, _, _ := st.CreateJob(ctx, store.Job{ItemID: "done", ItemName: "x", Plan: "{}", LocalPath: "/a/movies/Old/old.mkv", SourceSize: ptr(3 * gb)})
			must(t, st.FinishJob(ctx, done, store.JobOutcome{Status: store.JobComplete, BackupPath: "/a/movies/Old/.old.mkv.jellytrim-bak-1"}))

			o, err := newTestQueue(st, space).SpaceOutlook(ctx)
			must(t, err)
			if len(o.Filesystems) != 2 {
				t.Fatalf("filesystems %+v", o.Filesystems)
			}
			a, b := o.Filesystems[0], o.Filesystems[1]
			if a.Device != 1 || a.Path != "/a" || a.Jobs != 3 || a.Free != 10*gb || a.Backups != 3*gb || a.BackupCount != 1 {
				t.Errorf("filesystem a %+v", a)
			}
			if a.Peak != tc.wantPeakA || a.AtRisk != tc.wantRiskA || o.AtRisk != tc.wantRiskA {
				t.Errorf("filesystem a peak %d risk %v (outlook %v), want %d %v", a.Peak, a.AtRisk, o.AtRisk, tc.wantPeakA, tc.wantRiskA)
			}
			if b.Device != 2 || b.Path != "/b/movies/Three" || b.Jobs != 1 || b.Peak != 9*gb || b.AtRisk {
				t.Errorf("filesystem b %+v", b)
			}
		})
	}
}

func TestSpaceOutlookUsesProbeDevice(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	seedCandidates(t, st)
	must(t, st.SaveProbe(ctx, store.Probe{ItemID: "film", LocalPath: "/a/film/film.mkv", FileIdentity: store.FileIdentity{Dev: 7}}))
	_, _, err := st.CreateJob(ctx, store.Job{ItemID: "film", ItemName: "Film", Plan: "{}", LocalPath: "/a/film/film.mkv", EstMax: ptr(gb)})
	must(t, err)
	// The fake knows free space but not devices: the probe's device is used.
	o, err := newTestQueue(st, fakeSpace{free: map[string]uint64{"/a": uint64(50 * gb)}}).SpaceOutlook(ctx)
	must(t, err)
	if len(o.Filesystems) != 1 || o.Filesystems[0].Device != 7 || o.Filesystems[0].Free != 50*gb {
		t.Fatalf("outlook %+v", o.Filesystems)
	}
}

func TestDispatcherHoldsJobForSpace(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	must(t, st.SetSetting(ctx, store.KeyDryRun, "false"))
	space := fakeSpace{
		free: map[string]uint64{"/full": uint64(5 * gb), "/roomy": uint64(500 * gb)},
		dev:  map[string]uint64{"/full": 1, "/roomy": 2},
	}
	big, _, err := st.CreateJob(ctx, store.Job{ItemID: "big", ItemName: "Big Film", Plan: "{}", LocalPath: "/full/big/big.mkv",
		SourceSize: ptr(60 * gb), EstMax: ptr(10 * gb)})
	must(t, err)
	q := newTestQueue(st, space)

	// The only job does not fit: it stays waiting and the hold says why.
	q.startReady(ctx)
	if j, _ := st.Job(ctx, big); j.Status != store.JobWaiting {
		t.Fatalf("job started without space: %s", j.Status)
	}
	h, ok := q.SpaceHold()
	if !ok || h.JobID != big || h.Free != 5*gb || h.Need != startNeed(10*gb) || !strings.Contains(h.Reason(), "Big Film") {
		t.Fatalf("hold %+v %v", h, ok)
	}
	// While paused nothing is held: nothing would start anyway.
	q.SetPaused(true)
	q.startReady(ctx)
	if _, held := q.SpaceHold(); held {
		t.Fatal("a paused queue reports a job held for space")
	}
	q.SetPaused(false)

	// A smaller saving on a filesystem with room may start meanwhile.
	small, _, err := st.CreateJob(ctx, store.Job{ItemID: "small", ItemName: "Small", Plan: "{}", LocalPath: "/roomy/small/small.mkv",
		SourceSize: ptr(2 * gb), EstMax: ptr(gb)})
	must(t, err)
	j, r, ok := q.nextStartable(ctx)
	if !ok || j.ID != small || r.dev != 2 || r.need != startNeed(gb) {
		t.Fatalf("next startable %d %+v %v", j.ID, r, ok)
	}
	if h, ok := q.SpaceHold(); !ok || h.JobID != big {
		t.Fatalf("hold after passing over %+v %v", h, ok)
	}

	// A running job's reservation counts against the same filesystem.
	q.mu.Lock()
	q.reserved[99] = reservation{dev: 2, need: 499 * gb}
	q.mu.Unlock()
	if _, _, ok := q.nextStartable(ctx); ok {
		t.Fatal("started a job into space a running job needs")
	}

	// Once space is freed the hold clears.
	q.mu.Lock()
	delete(q.reserved, 99)
	q.mu.Unlock()
	space.free["/full"] = uint64(100 * gb)
	if j, _, ok := q.nextStartable(ctx); !ok || j.ID != big {
		t.Fatalf("next %d %v", j.ID, ok)
	}
	if _, held := q.SpaceHold(); held {
		t.Fatal("hold not cleared")
	}
}
