package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/freakyturtle/jellytrim/internal/encoder"
	"github.com/freakyturtle/jellytrim/internal/ffmpeg"
	"github.com/freakyturtle/jellytrim/internal/pipeline"
	"github.com/freakyturtle/jellytrim/internal/queue"
	"github.com/freakyturtle/jellytrim/internal/store"
)

// queueTestEnv is the fixture library with a queue that is never started,
// so no real encode runs. Jobs are seeded straight into the store.
type queueTestEnv struct {
	libraryTestEnv
	q *queue.Service
}

func newQueueTestEnv(t *testing.T) queueTestEnv {
	t.Helper()
	e := newLibraryTestEnv(t)
	q := queue.New(queue.Options{
		Store: e.st, Library: e.lib,
		Registry: encoder.NewRegistry(encoder.DefaultBackends(encoder.DefaultQSVDevice)),
		Runner:   ffmpeg.New("ffmpeg", "ffprobe", nil),
		Now:      e.srv.Now,
	})
	srv := New(Deps{Store: e.st, Library: e.lib, Queue: q, Now: e.srv.Now})
	e.srv, e.h = srv, srv.Handler()
	return queueTestEnv{libraryTestEnv: e, q: q}
}

// post sends a form and checks that nothing secret leaked into the body.
func (e queueTestEnv) post(t *testing.T, path string, hx bool) (*http.Response, string) {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(url.Values{}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if hx {
		req.Header.Set("HX-Request", "true")
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	res := rec.Result()
	body, _ := io.ReadAll(res.Body)
	if bytes.Contains(body, []byte(e.jf.APIKey())) {
		t.Fatalf("%s: the API key appears in the response", path)
	}
	return res, string(body)
}

type queueTestJob struct {
	item, name string
	status     string // final or running status; "" leaves it waiting
	outcome    store.JobOutcome
	progress   float64
	local      string // the file's local path; a made-up one when empty
	backup     bool   // set the outcome's backup to the pipeline's backup path
}

// seed queues a job and moves it to the wanted status.
func (e queueTestEnv) seed(t *testing.T, j queueTestJob) int64 {
	t.Helper()
	ctx := context.Background()
	src, lo, hi := int64(48_200_000_000), int64(12_000_000_000), int64(15_000_000_000)
	local := j.local
	if local == "" {
		local = "/data/films/" + j.name + ".mkv"
	}
	id, created, err := e.st.CreateJob(ctx, store.Job{
		ItemID: j.item, ItemName: j.name, LibraryName: "Films", PolicyName: "Efficient encoding", Trigger: "auto",
		Plan: `{"quality":"high"}`, LocalPath: local,
		SourceSummary: "2160p H.264", TargetSummary: "1080p HEVC", SourceSize: &src, EstMin: &lo, EstMax: &hi,
	})
	libraryTestMust(t, err)
	if !created {
		t.Fatalf("job for %s not created", j.item)
	}
	if j.status == "" || j.status == store.JobWaiting {
		return id
	}
	if _, err := e.st.StartJob(ctx, id, "", "Software (x265)"); err != nil {
		t.Fatal(err)
	}
	switch j.status {
	case store.JobAnalysing, store.JobEncoding, store.JobValidating, store.JobReplacing:
		libraryTestMust(t, e.st.SetJobStatus(ctx, id, j.status))
		eta := int64(720)
		libraryTestMust(t, e.st.SetJobProgress(ctx, id, j.progress, 2.4, &eta))
	default:
		o := j.outcome
		o.Status = j.status
		if j.backup {
			o.BackupPath = pipeline.BackupPath(local, id)
		}
		libraryTestMust(t, e.st.FinishJob(ctx, id, o))
	}
	return id
}

func queueDiag(t *testing.T, d pipeline.Diagnostics) string {
	t.Helper()
	b, err := json.Marshal(d)
	libraryTestMust(t, err)
	return string(b)
}

func queueExpect(t *testing.T, body string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(body, templEscape(w)) {
			t.Errorf("missing %q", w)
		}
	}
}

// queueExpectRaw looks for markup, which templEscape would mangle.
func queueExpectRaw(t *testing.T, body string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("missing markup %q", w)
		}
	}
}

func queueExpectNot(t *testing.T, body string, notWant ...string) {
	t.Helper()
	for _, w := range notWant {
		if strings.Contains(body, templEscape(w)) {
			t.Errorf("should not contain %q", w)
		}
	}
}

func TestQueuePageRunningAndWaiting(t *testing.T) {
	e := newQueueTestEnv(t)
	run := e.seed(t, queueTestJob{item: "a", name: "Alpha (2019)", status: store.JobEncoding, progress: 0.38})
	wait := e.seed(t, queueTestJob{item: "b", name: "Bravo (2020)"})

	res, body := e.get(t, "/queue", false)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	queueExpect(t, body,
		"Alpha (2019)", "Films", "2160p H.264 → 1080p HEVC · High", "Encoding",
		"38% · 2.4× · 12 min left", "Software (x265)", "Efficient encoding", "Est. saving", "33.2 to 36.2 GB",
		"Stop this job?", "The original file is not affected.", "/queue/"+strconv.FormatInt(run, 10)+"/cancel",
		"Bravo (2020)", "/queue/"+strconv.FormatInt(wait, 10)+"/cancel", "Dry Run is on. Nothing is processed.",
		"Nothing runs while Dry Run is on", "Pause queue", "hx-trigger=",
	)
	queueExpectRaw(t, body, `value="38"`, `aria-current="step"`)

	_, body = e.get(t, "/queue/live", true)
	if strings.Contains(body, "<html") || !strings.Contains(body, `id="queue-live"`) {
		t.Fatal("the live fragment is a full page or is missing its container")
	}
	// A stage change since the browser last looked is announced once.
	_, body = e.get(t, "/queue/live?was="+url.QueryEscape(strconv.FormatInt(run, 10)+":Analysing"), true)
	queueExpect(t, body, "Alpha (2019): Encoding.")
	queueExpectRaw(t, body, `hx-swap-oob="innerHTML"`)
	_, body = e.get(t, "/queue/live?was="+url.QueryEscape(strconv.FormatInt(run, 10)+":Encoding"), true)
	queueExpectNot(t, body, "hx-swap-oob")
}

func TestQueuePageIdleStopsPolling(t *testing.T) {
	e := newQueueTestEnv(t)
	_, body := e.get(t, "/queue", false)
	queueExpect(t, body, "No jobs in the queue", "Review policies")
	queueExpectNot(t, body, "hx-trigger=")
}

func TestQueuePauseResume(t *testing.T) {
	e := newQueueTestEnv(t)
	res, _ := e.post(t, "/queue/pause", false)
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/queue" {
		t.Fatalf("pause: status %d, location %q", res.StatusCode, res.Header.Get("Location"))
	}
	if !e.q.Paused() {
		t.Fatal("the queue is not paused")
	}
	libraryTestMust(t, e.st.SetSetting(context.Background(), store.KeyDryRun, "false"))
	_, body := e.get(t, "/queue", false)
	queueExpect(t, body, "Resume queue", "Paused. Nothing new starts until you resume.")

	res, body = e.post(t, "/queue/resume", true)
	if res.StatusCode != http.StatusOK || strings.Contains(body, "<html") {
		t.Fatalf("resume over HTMX: status %d", res.StatusCode)
	}
	queueExpect(t, body, "Pause queue", "Running. 1 job at a time.")
	if e.q.Paused() {
		t.Fatal("the queue is still paused")
	}
}

func TestQueueCancelWaitingJob(t *testing.T) {
	e := newQueueTestEnv(t)
	id := e.seed(t, queueTestJob{item: "b", name: "Bravo (2020)"})
	path := "/queue/" + strconv.FormatInt(id, 10) + "/cancel"

	res, _ := e.post(t, path, false)
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/queue?notice=cancelled" {
		t.Fatalf("cancel: status %d, location %q", res.StatusCode, res.Header.Get("Location"))
	}
	j, err := e.st.Job(context.Background(), id)
	libraryTestMust(t, err)
	if j.Status != store.JobCancelled {
		t.Fatalf("status %q, want cancelled", j.Status)
	}
	_, body := e.get(t, "/queue?notice=cancelled", false)
	queueExpect(t, body, "Job cancelled")

	// Cancelling again explains itself instead of failing.
	res, body = e.post(t, path, true)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("second cancel: status %d", res.StatusCode)
	}
	queueExpect(t, body, "That job could not be cancelled")
	queueExpectRaw(t, body, `role="alert"`)

	res, _ = e.post(t, "/queue/abc/cancel", false)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("bad id: status %d", res.StatusCode)
	}
}

func TestHistoryFiltersAndTotals(t *testing.T) {
	e := newQueueTestEnv(t)
	out := int64(13_700_000_000)
	e.seed(t, queueTestJob{item: "a", name: "Alpha (2019)", status: store.JobComplete,
		outcome: store.JobOutcome{OutputSize: &out, Summary: "Done."}})
	e.seed(t, queueTestJob{item: "b", name: "Bravo (2020)", status: store.JobSkipped,
		outcome: store.JobOutcome{Summary: "The new file would save only 3%, below the 10% minimum."}})
	e.seed(t, queueTestJob{item: "c", name: "Charlie (2021)", status: store.JobFailed,
		outcome: store.JobOutcome{Summary: "The encoder stopped with an error. The original is unchanged."}})
	e.seed(t, queueTestJob{item: "d", name: "Delta (2022)", status: store.JobCancelled,
		outcome: store.JobOutcome{Summary: "Cancelled before it started."}})
	e.seed(t, queueTestJob{item: "e", name: "Echo (2022)"}) // still waiting: not history

	_, body := e.get(t, "/history", false)
	queueExpect(t, body,
		"JellyTrim has saved 34.5 GB across 1 file.", "48.2 GB → 13.7 GB, saved 34.5 GB",
		"Alpha (2019)", "Bravo (2020)", "Charlie (2021)", "Delta (2022)", "below the 10% minimum",
		"Complete", "Skipped", "Failed", "Cancelled",
	)
	queueExpectRaw(t, body, `aria-current="page">All</a>`)
	queueExpectNot(t, body, "Echo (2022)")

	_, body = e.get(t, "/history?status=failed", false)
	queueExpect(t, body, "Charlie (2021)")
	queueExpectNot(t, body, "Alpha (2019)", "Bravo (2020)")

	_, body = e.get(t, "/history?status=bogus", false)
	queueExpect(t, body, "Alpha (2019)", "Delta (2022)")
}

func TestHistoryJobDetail(t *testing.T) {
	e := newQueueTestEnv(t)
	out := int64(13_700_000_000)
	exp := time.Date(2027, 1, 8, 12, 0, 0, 0, time.UTC)
	diag := queueDiag(t, pipeline.Diagnostics{
		Step: "replacing", Encoder: "Software (x265)", Speed: 2.4, EncodeTime: "12 min",
		Args:       []string{"-hide_banner", "-i", "/data/films/Alpha (2019)/Alpha (2019).mkv", "-c:v", "libx265", "/data/films/it's.mkv"},
		Checks:     []pipeline.Check{{Name: "container", Pass: true, Detail: "container matroska (expected matroska)"}, {Name: "decode", Pass: true, Detail: "decoded without errors"}},
		Warnings:   []string{"Could not set the file's group to match the original."},
		StderrTail: "frame=100 fps=24 q=28.0 size=1024kB",
	})
	done := e.seed(t, queueTestJob{item: "a", name: "Alpha (2019)", status: store.JobComplete, outcome: store.JobOutcome{
		OutputSize: &out, Summary: "Saved 34.5 GB.", Diagnostics: diag, BackupPath: "/data/films/.jellytrim-backup", BackupExpiresAt: &exp,
	}})
	failed := e.seed(t, queueTestJob{item: "c", name: "Charlie (2021)", status: store.JobFailed, outcome: store.JobOutcome{
		Summary:     "The encoder stopped with an error. The original is unchanged.",
		Diagnostics: queueDiag(t, pipeline.Diagnostics{Step: "encoding", Error: "exit status 1", StderrTail: "Conversion failed!"}),
	}})

	res, body := e.get(t, "/history/"+strconv.FormatInt(done, 10), false)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	queueExpect(t, body,
		"Complete. Saved 34.5 GB", "callout--ok", "Checks on the new file", "Container: container matroska (expected matroska)",
		"Decode: decoded without errors", "Could not set the file's group", "The exact ffmpeg command",
		"ffmpeg -hide_banner -i '/data/films/Alpha (2019)/Alpha (2019).mkv' -c:v libx265 '/data/films/it'\\''s.mkv'",
		"frame=100 fps=24", "Restore original", "Backup kept until 8 Jan 2027", "Put the original back?",
		"Software (x265)", "2.4× real time", "72%", "Automatic, after a sync",
	)
	if strings.Contains(body, ">Retry</button>") {
		t.Error("a complete job offers Retry")
	}

	_, body = e.get(t, "/history/"+strconv.FormatInt(failed, 10), false)
	queueExpect(t, body, "callout--bad", "Failed", "Conversion failed!", "exit status 1")
	queueExpectRaw(t, body, ">Retry</button>")
	queueExpectNot(t, body, "Restore original")

	res, _ = e.get(t, "/history/999", false)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("missing job: status %d", res.StatusCode)
	}
}

func TestHistoryRestoreErrorsAreFriendly(t *testing.T) {
	e := newQueueTestEnv(t)
	out := int64(13_700_000_000)
	noBackup := e.seed(t, queueTestJob{item: "a", name: "Alpha (2019)", status: store.JobComplete, outcome: store.JobOutcome{OutputSize: &out}})
	missing := e.seed(t, queueTestJob{item: "b", name: "Bravo (2020)", status: store.JobComplete, outcome: store.JobOutcome{
		OutputSize: &out, BackupPath: "/nonexistent/.jellytrim-backup.mkv",
	}})

	_, body := e.get(t, "/history/"+strconv.FormatInt(noBackup, 10), false)
	queueExpectNot(t, body, "Restore original")

	res, body := e.post(t, "/history/"+strconv.FormatInt(noBackup, 10)+"/restore", false)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("no backup: status %d", res.StatusCode)
	}
	queueExpect(t, body, "There is no backup to restore")

	res, body = e.post(t, "/history/"+strconv.FormatInt(missing, 10)+"/restore", false)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("missing backup file: status %d", res.StatusCode)
	}
	queueExpect(t, body, "JellyTrim could not put the original back", "Technical details")
}

func TestRetryFailedJobIsFriendly(t *testing.T) {
	e := newQueueTestEnv(t)
	alpha := e.itemID(t, "Alpha")
	id := e.seed(t, queueTestJob{item: alpha, name: "Alpha (2019)", status: store.JobFailed, outcome: store.JobOutcome{Summary: "Failed."}})
	gone := e.seed(t, queueTestJob{item: "no-such-item", name: "Gone", status: store.JobFailed})
	waiting := e.seed(t, queueTestJob{item: "w", name: "Waiting"})

	// Dry Run is on, so the item cannot be queued again.
	res, body := e.post(t, "/queue/"+strconv.FormatInt(id, 10)+"/retry", false)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("retry in Dry Run: status %d", res.StatusCode)
	}
	queueExpect(t, body, "JellyTrim could not queue this item again", "Dry Run is on")

	res, body = e.post(t, "/queue/"+strconv.FormatInt(gone, 10)+"/retry", false)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("retry of a removed item: status %d", res.StatusCode)
	}
	queueExpect(t, body, "Checking the file again did not work")

	res, body = e.post(t, "/queue/"+strconv.FormatInt(waiting, 10)+"/retry", false)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("retry of a waiting job: status %d", res.StatusCode)
	}
	queueExpect(t, body, "Only failed, skipped or cancelled jobs")

	// With Dry Run off it goes back in the queue.
	libraryTestMust(t, e.st.SetSetting(context.Background(), store.KeyDryRun, "false"))
	res, _ = e.post(t, "/queue/"+strconv.FormatInt(id, 10)+"/retry", false)
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/queue" {
		t.Fatalf("retry: status %d, location %q", res.StatusCode, res.Header.Get("Location"))
	}
}

func TestOptimiseItem(t *testing.T) {
	e := newQueueTestEnv(t)
	ctx := context.Background()
	alpha := e.itemID(t, "Alpha")
	itemPath := "/library/" + url.PathEscape(alpha)

	res, _ := e.post(t, itemPath+"/optimise", false)
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != itemPath+"?error=dryrun" {
		t.Fatalf("optimise in Dry Run: status %d, location %q", res.StatusCode, res.Header.Get("Location"))
	}
	_, body := e.get(t, itemPath+"?error=dryrun", false)
	queueExpect(t, body, "JellyTrim will not change files while Dry Run is on", "Dry Run is on. Turn it off in Settings to optimise files.")

	libraryTestMust(t, e.st.SetSetting(ctx, store.KeyDryRun, "false"))
	res, _ = e.post(t, "/library/"+url.PathEscape(e.itemID(t, "Bravo"))+"/optimise", false)
	if !strings.HasSuffix(res.Header.Get("Location"), "?error=notplanned") {
		t.Fatalf("optimise a protected item: location %q", res.Header.Get("Location"))
	}
	_, body = e.get(t, itemPath, false)
	queueExpectRaw(t, body, `action="`+itemPath+`/optimise"`, `type="submit">Optimise now`)

	res, _ = e.post(t, itemPath+"/optimise", true)
	if res.Header.Get("HX-Redirect") != "/queue" {
		t.Fatalf("optimise: HX-Redirect %q", res.Header.Get("HX-Redirect"))
	}
	jobs, err := e.st.ActiveJobs(ctx)
	libraryTestMust(t, err)
	if len(jobs) != 1 || jobs[0].ItemID != alpha || jobs[0].Trigger != "manual" {
		t.Fatalf("active jobs: %+v", jobs)
	}
	_, body = e.get(t, itemPath, false)
	queueExpect(t, body, "Queued", "View the queue")
	queueExpectNot(t, body, "Optimise now")

	res, _ = e.post(t, "/library/no-such-item/optimise", false)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("missing item: status %d", res.StatusCode)
	}
}

func TestIncludeItemClearsExclusion(t *testing.T) {
	e := newQueueTestEnv(t)
	ctx := context.Background()
	alpha := e.itemID(t, "Alpha")
	itemPath := "/library/" + url.PathEscape(alpha)
	libraryTestMust(t, e.st.ExcludeItem(ctx, alpha, "You restored the original, so JellyTrim leaves this item alone until you allow it again."))
	_, err := e.lib.Evaluate(ctx)
	libraryTestMust(t, err)

	_, body := e.get(t, itemPath, false)
	queueExpect(t, body, "Allow changes again", "Allow JellyTrim to change this item again")
	queueExpectNot(t, body, "Optimise now")

	res, _ := e.post(t, itemPath+"/include", false)
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != itemPath {
		t.Fatalf("include: status %d, location %q", res.StatusCode, res.Header.Get("Location"))
	}
	ex, err := e.st.Exclusions(ctx)
	libraryTestMust(t, err)
	if len(ex) != 0 {
		t.Fatalf("exclusions left: %v", ex)
	}
	_, body = e.get(t, itemPath, false)
	queueExpect(t, body, "Optimise now")
	queueExpectNot(t, body, "Allow changes again")
}

func TestHistoryAttentionAndKeptFiles(t *testing.T) {
	e := newQueueTestEnv(t)
	exp := time.Date(2027, 1, 8, 12, 0, 0, 0, time.UTC)
	att := e.seed(t, queueTestJob{item: "a", name: "Alpha (2019)", status: store.JobAttention, backup: true, outcome: store.JobOutcome{
		Summary: "JellyTrim stopped while replacing the file. The original is safe but not at its usual path.",
	}})
	skipped := e.seed(t, queueTestJob{item: "b", name: "Bravo (2020)", status: store.JobSkipped, backup: true, outcome: store.JobOutcome{
		Summary: "Another program replaced the file while JellyTrim was working.", BackupExpiresAt: &exp,
	}})

	_, body := e.get(t, "/history", false)
	queueExpect(t, body, "These jobs stopped in a state you need to look at", "Needs attention", "Bravo (2020)")
	if strings.Index(body, "Alpha (2019)") > strings.Index(body, "Bravo (2020)") {
		t.Error("the job that needs attention is not listed first")
	}
	_, body = e.get(t, "/history?status=attention", false)
	queueExpect(t, body, "Alpha (2019)")
	queueExpectNot(t, body, "Bravo (2020)", "These jobs stopped")

	_, body = e.get(t, "/history/"+strconv.FormatInt(att, 10), false)
	queueExpect(t, body, "callout--bad", "Needs attention", "The original is safe at", pipeline.BackupPath("/data/films/Alpha (2019).mkv", att))
	queueExpectNot(t, body, "Restore original")
	queueExpectRawNot(t, body, ">Retry</button>")

	_, body = e.get(t, "/history/"+strconv.FormatInt(skipped, 10), false)
	queueExpect(t, body, "Kept at", pipeline.BackupPath("/data/films/Bravo (2020).mkv", skipped), "until 8 Jan 2027.")
	queueExpectNot(t, body, "Restore original")
}

func TestHistoryRestoreRefusals(t *testing.T) {
	e := newQueueTestEnv(t)
	out := int64(13_700_000_000)
	// Another job for the same item is waiting, so restoring must wait.
	busy := e.seed(t, queueTestJob{item: "a", name: "Alpha (2019)", status: store.JobComplete, backup: true, outcome: store.JobOutcome{OutputSize: &out}})
	e.seed(t, queueTestJob{item: "a", name: "Alpha (2019)"})
	res, body := e.post(t, "/history/"+strconv.FormatInt(busy, 10)+"/restore", false)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("busy item: status %d", res.StatusCode)
	}
	queueExpect(t, body, "Another job is working on this item")

	// The file at the path is not the one JellyTrim wrote.
	dir := t.TempDir()
	local := filepath.Join(dir, "Bravo (2020).mkv")
	changed := e.seed(t, queueTestJob{item: "b", name: "Bravo (2020)", status: store.JobComplete, local: local, backup: true, outcome: store.JobOutcome{OutputSize: &out}})
	libraryTestMust(t, os.WriteFile(local, []byte("a new download"), 0o644))
	libraryTestMust(t, os.WriteFile(pipeline.BackupPath(local, changed), []byte("the original"), 0o644))
	res, body = e.post(t, "/history/"+strconv.FormatInt(changed, 10)+"/restore", false)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("changed file: status %d", res.StatusCode)
	}
	queueExpect(t, body, "The file has changed since JellyTrim optimised it")
	if b, _ := os.ReadFile(local); string(b) != "a new download" {
		t.Fatal("the file at the path was changed")
	}
}

func queueExpectRawNot(t *testing.T, body string, notWant ...string) {
	t.Helper()
	for _, w := range notWant {
		if strings.Contains(body, w) {
			t.Errorf("should not contain markup %q", w)
		}
	}
}
