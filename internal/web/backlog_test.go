package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/freakyturtle/jellytrim/internal/encoder"
	"github.com/freakyturtle/jellytrim/internal/queue"
	"github.com/freakyturtle/jellytrim/internal/store"
	"github.com/freakyturtle/jellytrim/internal/timetable"
	"github.com/freakyturtle/jellytrim/internal/units"
)

// backlogSpace is a filesystem with a fixed amount of free space, all on
// one device.
type backlogSpace struct{ free uint64 }

func (f backlogSpace) Free(string) (uint64, error) { return f.free, nil }
func (backlogSpace) Device(string) (uint64, error) { return 1, nil }

// backlogEnv is a set-up server with a queue that is not started, on a
// real store with no library.
type backlogEnv struct {
	st  *store.Store
	q   *queue.Service
	srv *Server
	h   http.Handler
}

func newBacklogEnv(t *testing.T, free uint64) backlogEnv {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir())
	libraryTestMust(t, err)
	t.Cleanup(func() { _ = st.Close() })
	libraryTestMust(t, st.SetSetting(ctx, store.KeySetupComplete, "true"))
	now := func() time.Time { return time.Date(2027, 1, 4, 12, 0, 0, 0, time.UTC) }
	q := queue.New(queue.Options{
		Store: st, Registry: encoder.NewRegistry(encoder.DefaultBackends(encoder.DefaultQSVDevice)),
		Space: backlogSpace{free: free}, Now: now, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	srv := New(Deps{Store: st, Queue: q, Now: now, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	return backlogEnv{st: st, q: q, srv: srv, h: srv.Handler()}
}

// seed queues n waiting jobs, each a two-hour film of 10 GB estimated at 3
// to 4 GB. durationMs 0 leaves the length unknown.
func (e backlogEnv) seed(t *testing.T, prefix string, n int, durationMs int64, trigger string) {
	t.Helper()
	jobs := make([]store.Job, 0, n)
	for i := range n {
		src, lo, hi := int64(10_000_000_000), int64(3_000_000_000), int64(4_000_000_000)
		name := prefix + " " + strconv.Itoa(i+1)
		jobs = append(jobs, store.Job{
			ItemID: prefix + "-" + strconv.Itoa(i), ItemName: name, LibraryName: "Films", Trigger: trigger,
			Plan: `{"encoder":"x265","quality":"high"}`, LocalPath: "/data/films/" + name + ".mkv",
			SourceSummary: "1080p H.264", TargetSummary: "1080p HEVC",
			SourceSize: &src, EstMin: &lo, EstMax: &hi, DurationMs: durationMs,
		})
	}
	created, err := e.st.CreateJobs(context.Background(), jobs)
	libraryTestMust(t, err)
	if created != n {
		t.Fatalf("created %d jobs, want %d", created, n)
	}
}

// finish records a complete job, with an encode speed and optionally a
// backup of size bytes.
func (e backlogEnv) finish(t *testing.T, item string, speed float64, backup int64) {
	t.Helper()
	ctx := context.Background()
	id, _, err := e.st.CreateJob(ctx, store.Job{
		ItemID: item, ItemName: item, Trigger: "auto", Plan: `{"encoder":"x265"}`,
		LocalPath: "/data/films/" + item + ".mkv", SourceSize: &backup,
	})
	libraryTestMust(t, err)
	_, err = e.st.StartJob(ctx, id, "", "Software (x265)")
	libraryTestMust(t, err)
	o := store.JobOutcome{Status: store.JobComplete, Diagnostics: `{"speed":` + strconv.FormatFloat(speed, 'f', 1, 64) + `}`}
	if backup > 0 {
		o.BackupPath = "/data/films/.jellytrim/" + item + ".mkv"
	}
	libraryTestMust(t, e.st.FinishJob(ctx, id, o))
}

func (e backlogEnv) schedule(t *testing.T, w timetable.Week) {
	t.Helper()
	libraryTestMust(t, e.st.SetSetting(context.Background(), store.KeyProcessingSchedule, w.String()))
}

func (e backlogEnv) get(t *testing.T, path string) string {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: status %d", path, rec.Code)
	}
	return rec.Body.String()
}

func TestQueueBacklogWording(t *testing.T) {
	nights := timetable.Presets()[1].Week
	cases := []struct {
		name    string
		week    timetable.Week
		history bool
		unknown int
		want    []string
		raw     string // the time value, as markup
		wantNot []string
	}{
		{
			name: "any time, no history", week: timetable.Always(),
			// 3 two-hour films at the default software speed of 1x.
			raw:     `> 6 <span class="metric__unit">hours`,
			want:    []string{"Estimated encoding time. The schedule allows any hour.", "hours", "Rough estimate, based on typical speeds, until JellyTrim has finished a few jobs."},
			wantNot: []string{"no known length", "Change the processing schedule"},
		},
		{
			name: "limited hours, with history", week: nights, history: true, unknown: 2,
			// 3 films at 2x is 3 hours of encoding, at 42 hours a week of 168.
			raw: `> 12 <span class="metric__unit">hours`,
			want: []string{"Estimate at your 42 active hours a week.", "hours", "Rough estimate, based on the speed of recent jobs.",
				"2 files have no known length and are not counted.", "Change the processing schedule"},
			wantNot: []string{"typical speeds"},
		},
		{
			name: "no hours", week: timetable.Week{},
			want: []string{"No processing hours are switched on, so nothing will run.", "Change the processing schedule"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newBacklogEnv(t, 1<<50)
			e.seed(t, "Film", 3, 2*3600*1000, "auto")
			if c.unknown > 0 {
				e.seed(t, "Clip", c.unknown, 0, "auto")
			}
			if c.history {
				e.finish(t, "done", 2, 0)
			}
			e.schedule(t, c.week)
			body := e.get(t, "/queue")
			files := strconv.Itoa(3 + c.unknown)
			queueExpect(t, body, "Backlog", "Waiting", "To save", "Time to finish", "Estimate: ", " GB of files.")
			queueExpectRaw(t, body, `id="queue-backlog-body" hx-get="/queue/backlog" hx-trigger="every 60s"`, files+` <span class="metric__unit">files`)
			queueExpect(t, body, c.want...)
			queueExpectNot(t, body, c.wantNot...)
			if c.raw != "" {
				queueExpectRaw(t, body, c.raw)
			}
		})
	}
}

func TestBacklogDuration(t *testing.T) {
	cases := []struct {
		d           time.Duration
		value, unit string
	}{
		{10 * time.Minute, "1", "hour"},
		{36 * time.Hour, "36", "hours"},
		{50 * time.Hour, "2", "days"},
		{20 * 24 * time.Hour, "20", "days"},
		{21 * 24 * time.Hour, "3", "weeks"},
		{83 * 24 * time.Hour, "12", "weeks"},
		{100 * 24 * time.Hour, "3", "months"},
	}
	for _, c := range cases {
		v, u := backlogDuration(c.d)
		if v != c.value || u != c.unit {
			t.Errorf("%v: got %s %s, want %s %s", c.d, v, u, c.value, c.unit)
		}
	}
}

func TestQueueBacklogHiddenWhenEmptyAndOutOfPoll(t *testing.T) {
	e := newBacklogEnv(t, 1<<50)
	body := e.get(t, "/queue")
	queueExpectNot(t, body, "Time to finish", "every 60s")

	e.seed(t, "Film", 2, 3600*1000, "auto")
	// The 2-second fragment keeps an empty slot that htmx preserves, and
	// never totals the backlog.
	body = e.get(t, "/queue/live")
	queueExpectRaw(t, body, `<div class="queue-backlog" id="queue-backlog" hx-preserve="true"></div>`)
	queueExpectNot(t, body, "Time to finish")
	// The minute refresh has the estimate.
	body = e.get(t, "/queue/backlog")
	queueExpect(t, body, "Time to finish")
	if strings.Contains(body, "<html") {
		t.Fatal("the backlog fragment is a full page")
	}
}

func TestQueueWaitingPaging(t *testing.T) {
	e := newBacklogEnv(t, 1<<50)
	e.seed(t, "Auto", 249, 3600*1000, "auto")
	e.seed(t, "Manual", 1, 3600*1000, "manual")

	body := e.get(t, "/queue")
	queueExpect(t, body, "Showing the first 100 of 250 waiting jobs.", "Manual jobs first, then the biggest savings.", "Show more")
	queueExpectRaw(t, body, `href="/queue?waiting=200#queue-waiting"`, `hx-vals="{&#34;waiting&#34;:&#34;100&#34;}"`)
	if n := strings.Count(body, `id="queue-waiting-cancel-`); n != 100 {
		t.Errorf("listed %d waiting jobs, want 100", n)
	}
	if first := strings.Index(body, "Manual 1"); first < 0 || first > strings.Index(body, "Auto 1") {
		t.Error("the manual job is not listed first")
	}

	body = e.get(t, "/queue/live?waiting=200")
	queueExpect(t, body, "Showing the first 200 of 250 waiting jobs.")
	queueExpectRaw(t, body, `href="/queue?waiting=300#queue-waiting"`)
	if n := strings.Count(body, `id="queue-waiting-cancel-`); n != 200 {
		t.Errorf("listed %d waiting jobs, want 200", n)
	}

	body = e.get(t, "/queue?waiting=300")
	queueExpect(t, body, "250 jobs waiting.")
	queueExpectNot(t, body, "Show more")

	for in, want := range map[string]int{"": 100, "abc": 100, "-5": 100, "150": 200, "200": 200, "5000": 1000} {
		if got := queueWaitingLimit(in); got != want {
			t.Errorf("queueWaitingLimit(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestQueueActionKeepsWaitingLength(t *testing.T) {
	e := newBacklogEnv(t, 1<<50)
	e.seed(t, "Film", 150, 3600*1000, "auto")
	jobs, err := e.st.NextWaitingJobs(context.Background(), 1)
	libraryTestMust(t, err)

	form := url.Values{"waiting": {"200"}}
	req := httptest.NewRequest("POST", "/queue/"+strconv.FormatInt(jobs[0].ID, 10)+"/cancel", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	body := rec.Body.String()
	queueExpect(t, body, "149 jobs waiting.", "Job cancelled")
	// The backlog is refreshed out of band, since the action changed it.
	queueExpectRaw(t, body, `hx-swap-oob="true"`, `149 <span class="metric__unit">files`)
}

func TestQueueSpaceHoldCallout(t *testing.T) {
	e := newBacklogEnv(t, 1<<30) // 1 GiB free: no job fits
	e.seed(t, "Film", 2, 3600*1000, "auto")
	e.schedule(t, timetable.Always())
	libraryTestMust(t, e.st.SetSetting(context.Background(), store.KeyDryRun, "false"))

	ctx, cancel := context.WithCancel(context.Background())
	e.q.Start(ctx)
	t.Cleanup(func() { cancel(); e.q.Stop() })
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, ok := e.q.SpaceHold(); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the queue did not hold the job for space")
		}
		time.Sleep(10 * time.Millisecond)
	}
	h, _ := e.q.SpaceHold()

	body := e.get(t, "/queue")
	queueExpect(t, body, "The next job is waiting for free space", h.Reason(),
		"free some space on that drive, or keep backups for fewer days", "Change how long backups are kept")
	queueExpectRaw(t, body, `href="/settings#safety"`)

	// The Dashboard lists it as a problem too.
	body = e.get(t, "/")
	queueExpect(t, body, h.Reason(), "Open the queue")
}

func TestDashboardSpaceAtRisk(t *testing.T) {
	free := uint64(20_000_000_000)
	e := newBacklogEnv(t, free)
	e.seed(t, "Film", 10, 3600*1000, "auto") // 10 x 4 GB while backups are kept

	body := e.get(t, "/")
	want := "The queue needs about " + units.Bytes(40_000_000_000) + " on /data/films, but only " +
		units.Bytes(int64(free)) + " is free while backups are kept for 7 days."
	queueExpect(t, body, want, "Change how long backups are kept", "Open the queue")
	queueExpectRaw(t, body, `href="/settings#safety"`, `href="/queue"`)

	// The Queue page warns in its backlog.
	body = e.get(t, "/queue")
	queueExpect(t, body, "The queue may run out of space", want)

	// With plenty of room there is no warning.
	e2 := newBacklogEnv(t, 1<<50)
	e2.seed(t, "Film", 10, 3600*1000, "auto")
	queueExpectNot(t, e2.get(t, "/"), "The queue needs about")
}

func TestSettingsBackupUseHint(t *testing.T) {
	e := newBacklogEnv(t, 1<<50)
	body := e.get(t, "/settings")
	queueExpect(t, body, "No backups are kept at the moment.", "For a large first run, a short retention (0 or 1 day) frees space sooner.")
	queueExpectRaw(t, body, `aria-describedby="settings-backup-days-hint settings-backup-days-note"`)

	e.finish(t, "one", 2, 48_200_000_000)
	e.finish(t, "two", 2, 1_800_000_000)
	body = e.get(t, "/settings")
	queueExpect(t, body, "Backups currently hold "+units.Bytes(50_000_000_000)+" in 2 files.")
}
