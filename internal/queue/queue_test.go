package queue

import (
	"context"
	"crypto/sha256"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/freakyturtle/jellytrim/internal/encoder"
	"github.com/freakyturtle/jellytrim/internal/ffmpeg"
	"github.com/freakyturtle/jellytrim/internal/jellyfin/jellyfintest"
	"github.com/freakyturtle/jellytrim/internal/library"
	"github.com/freakyturtle/jellytrim/internal/pipeline"
	"github.com/freakyturtle/jellytrim/internal/store"
	"github.com/freakyturtle/jellytrim/internal/testutil"
	"github.com/freakyturtle/jellytrim/internal/timetable"
)

type env struct {
	st    *store.Store
	lib   *library.Service
	q     *Service
	jf    *jellyfintest.Server
	alpha string
	hash  [32]byte
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(to)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	_ = out.Close()
}

func setup(t *testing.T) env {
	t.Helper()
	ffmpegPath, ffprobePath := testutil.RequireFFmpeg(t)
	notifyPoll, notifyDeadline = 10*time.Millisecond, 50*time.Millisecond
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	root := t.TempDir()
	alpha := filepath.Join(root, "movies", "Alpha (2019)", "Alpha (2019).mkv")
	copyFile(t, testutil.Fixture(t, "h264-1080p"), alpha)
	orig, _ := os.ReadFile(alpha)

	jf := jellyfintest.New(t, jellyfintest.WithFixtures())
	runner := ffmpeg.New(ffmpegPath, ffprobePath, nil)
	reg := encoder.NewRegistry(encoder.DefaultBackends(encoder.DefaultQSVDevice))
	reg.Detect(ctx, runner)
	lib := library.New(library.Options{Store: st, Prober: runner, Encoders: reg})
	if _, err := lib.SaveConnection(ctx, jf.URL, jf.APIKey()); err != nil {
		t.Fatal(err)
	}
	must(t, lib.RefreshServerInfo(ctx))
	libs, _ := st.Libraries(ctx)
	var ids []string
	for _, l := range libs {
		ids = append(ids, l.ID)
	}
	must(t, st.SetManagedLibraries(ctx, ids))
	must(t, st.SetSelectedUsers(ctx, []string{jellyfintest.FixtureUserID}))
	must(t, st.ReplacePathMappings(ctx, []store.PathMapping{{JellyfinPrefix: "/media/movies", LocalPrefix: filepath.ToSlash(filepath.Join(root, "movies"))}}))
	must(t, lib.CreateStarterPolicies(ctx, nil))
	rows, _ := st.Policies(ctx)
	for _, p := range rows {
		must(t, st.SetPolicyEnabled(ctx, p.ID, true))
	}
	must(t, st.SetSettings(ctx, map[string]string{store.KeyMinSavingPercent: "5", store.KeySetupComplete: "true"}))
	_, err = lib.Run(ctx)
	must(t, err)
	q := New(Options{Store: st, Library: lib, Registry: reg, Runner: runner})
	return env{st: st, lib: lib, q: q, jf: jf, alpha: alpha, hash: sha256.Sum256(orig)}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func alphaID(t *testing.T, e env) string {
	rows, _, err := e.st.LibraryList(context.Background(), store.LibraryFilter{Search: "Alpha", Limit: 1})
	must(t, err)
	return rows[0].Item.ID
}

func waitFor(t *testing.T, e env, jobID int64, statuses ...string) store.Job {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		j, err := e.st.Job(context.Background(), jobID)
		must(t, err)
		for _, s := range statuses {
			if j.Status == s {
				return j
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("job %d did not reach %v", jobID, statuses)
	return store.Job{}
}

func TestDryRunBlocksQueueing(t *testing.T) {
	e := setup(t)
	if _, _, err := e.q.Enqueue(context.Background(), alphaID(t, e), "manual"); err != ErrDryRun {
		t.Fatalf("err %v", err)
	}
	if n, err := e.q.EnqueueMatching(context.Background()); err != nil || n != 0 {
		t.Fatalf("queued %d in Dry Run (%v)", n, err)
	}
}

func TestOptimiseRestoreAndExclude(t *testing.T) {
	e := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); e.q.Stop(); e.lib.Wait() }()
	must(t, e.st.SetSetting(ctx, store.KeyDryRun, "false"))
	e.q.Start(ctx)
	id := alphaID(t, e)
	jobID, created, err := e.q.Enqueue(ctx, id, "manual")
	if err != nil || !created {
		t.Fatalf("enqueue: %v %v", created, err)
	}
	if _, again, _ := e.q.Enqueue(ctx, id, "manual"); again {
		t.Fatal("an item with an active job was queued twice")
	}
	j := waitFor(t, e, jobID, store.JobComplete, store.JobFailed, store.JobSkipped)
	if j.Status != store.JobComplete {
		t.Fatalf("job %s: %s\n%s", j.Status, j.Summary, j.Diagnostics)
	}
	if j.BackupPath == "" || j.OutputSize == nil || *j.OutputSize >= *j.SourceSize {
		t.Fatalf("job %+v", j)
	}
	b, _ := os.ReadFile(j.BackupPath)
	if sha256.Sum256(b) != e.hash {
		t.Fatal("backup does not hold the original")
	}
	// Jellyfin was told about the Jellyfin-side path.
	deadline := time.Now().Add(5 * time.Second)
	for len(e.jf.MediaUpdates()) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	ups := e.jf.MediaUpdates()
	if len(ups) == 0 || ups[0].Path != "/media/movies/Alpha (2019)/Alpha (2019).mkv" {
		t.Fatalf("media updates %+v", ups)
	}
	saved, n, _ := e.st.SavedBytes(ctx)
	if n != 1 || saved <= 0 {
		t.Fatalf("saved %d over %d jobs", saved, n)
	}

	e.lib.Wait()
	must(t, e.q.Restore(ctx, jobID, false))
	b, _ = os.ReadFile(e.alpha)
	if sha256.Sum256(b) != e.hash {
		t.Fatal("restore did not put the original back")
	}
	cancel()
	e.q.Stop()
	e.lib.Wait()
	bg := context.Background()
	if _, err := e.lib.Evaluate(bg); err != nil {
		t.Fatal(err)
	}
	ev, _ := e.st.Evaluation(bg, id)
	if ev.Outcome != "protected" {
		t.Fatalf("a restored item must be left alone, got %s", ev.Outcome)
	}
}

func TestRecoveryRequeuesAndCleansUp(t *testing.T) {
	e := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); e.q.Stop(); e.lib.Wait() }()
	must(t, e.st.SetSetting(ctx, store.KeyDryRun, "false"))
	id := alphaID(t, e)
	jobID, _, err := e.q.Enqueue(ctx, id, "manual")
	must(t, err)
	// Simulate a crash mid-encode: running status, a partial file and its
	// journal entry, nothing else.
	ok, err := e.st.StartJob(ctx, jobID, "", "")
	if err != nil || !ok {
		t.Fatal(err)
	}
	must(t, e.st.SetJobStatus(ctx, jobID, store.JobEncoding))
	partial := pipeline.PartialPath(e.alpha, jobID)
	must(t, os.WriteFile(partial, []byte("half an encode"), 0o644))
	_, err = e.st.JournalRecord(ctx, jobID, pipeline.StepPartialCreated, partial, e.alpha)
	must(t, err)

	e.q.SetPaused(true) // recover only; do not run the job again yet
	e.q.Start(ctx)
	j, _ := e.st.Job(ctx, jobID)
	if j.Status != store.JobWaiting {
		t.Fatalf("status after recovery %s", j.Status)
	}
	if _, err := os.Stat(partial); !os.IsNotExist(err) {
		t.Fatal("recovery left the partial file")
	}
	b, _ := os.ReadFile(e.alpha)
	if sha256.Sum256(b) != e.hash {
		t.Fatal("original changed")
	}
}

func TestRestoreRefusedWhileItemBusy(t *testing.T) {
	e := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); e.q.Stop(); e.lib.Wait() }()
	must(t, e.st.SetSetting(ctx, store.KeyDryRun, "false"))
	e.q.Start(ctx)
	id := alphaID(t, e)
	jobID, _, err := e.q.Enqueue(ctx, id, "manual")
	must(t, err)
	j := waitFor(t, e, jobID, store.JobComplete, store.JobFailed, store.JobSkipped)
	if j.Status != store.JobComplete || j.OutputIdentity == "" {
		t.Fatalf("job %s (%s), output identity %q", j.Status, j.Summary, j.OutputIdentity)
	}
	unlock, ok := e.q.lockItem(id)
	if !ok {
		t.Fatal("could not take the item lock")
	}
	if err := e.q.Restore(ctx, jobID, false); err != ErrItemBusy {
		t.Fatalf("restore while busy: %v", err)
	}
	unlock()
	must(t, e.q.Restore(ctx, jobID, false))
	b, _ := os.ReadFile(e.alpha)
	if sha256.Sum256(b) != e.hash {
		t.Fatal("restore did not put the original back")
	}
}

func TestScheduleBlocksNewJobs(t *testing.T) {
	e := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); e.q.Stop(); e.lib.Wait() }()
	must(t, e.st.SetSettings(ctx, map[string]string{
		store.KeyDryRun:             "false",
		store.KeyProcessingSchedule: timetable.Week{}.String(), // no active hours
	}))
	e.q.Start(ctx)
	jobID, _, err := e.q.Enqueue(ctx, alphaID(t, e), "manual")
	must(t, err)
	time.Sleep(300 * time.Millisecond)
	if j, _ := e.st.Job(ctx, jobID); j.Status != store.JobWaiting {
		t.Fatalf("a job started outside the schedule: %s", j.Status)
	}
	// Opening the schedule lets it run.
	must(t, e.st.SetSetting(ctx, store.KeyProcessingSchedule, timetable.Always().String()))
	e.q.Wake()
	if j := waitFor(t, e, jobID, store.JobComplete, store.JobFailed, store.JobSkipped); j.Status != store.JobComplete {
		t.Fatalf("%s: %s", j.Status, j.Summary)
	}
}

func TestScheduleStopsRunningJob(t *testing.T) {
	e := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); e.q.Stop(); e.lib.Wait() }()
	// A slow preset keeps the encode running long enough to stop it.
	must(t, e.st.SetSettings(ctx, map[string]string{store.KeyDryRun: "false", store.KeyX265Preset: "slower"}))
	e.q.Start(ctx)
	jobID, _, err := e.q.Enqueue(ctx, alphaID(t, e), "manual")
	must(t, err)
	waitFor(t, e, jobID, store.JobEncoding)

	must(t, e.st.SetSetting(ctx, store.KeyProcessingSchedule, timetable.Week{}.String()))
	e.q.Wake()
	deadline := time.Now().Add(30 * time.Second)
	var j store.Job
	for time.Now().Before(deadline) {
		j, _ = e.st.Job(ctx, jobID)
		if j.Status == store.JobWaiting && j.StartedAt == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if j.Status != store.JobWaiting || !strings.Contains(j.Summary, "processing schedule") {
		t.Fatalf("job %s: %q", j.Status, j.Summary)
	}
	b, _ := os.ReadFile(e.alpha)
	if sha256.Sum256(b) != e.hash {
		t.Fatal("original changed")
	}
	entries, _ := os.ReadDir(filepath.Dir(e.alpha))
	for _, en := range entries {
		if strings.Contains(en.Name(), ".jellytrim-") {
			t.Fatalf("left behind %s", en.Name())
		}
	}
}
