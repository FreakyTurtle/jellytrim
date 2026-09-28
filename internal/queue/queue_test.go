package queue

import (
	"context"
	"crypto/sha256"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/freakyturtle/jellytrim/internal/encoder"
	"github.com/freakyturtle/jellytrim/internal/ffmpeg"
	"github.com/freakyturtle/jellytrim/internal/jellyfin/jellyfintest"
	"github.com/freakyturtle/jellytrim/internal/library"
	"github.com/freakyturtle/jellytrim/internal/pipeline"
	"github.com/freakyturtle/jellytrim/internal/store"
	"github.com/freakyturtle/jellytrim/internal/testutil"
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
	must(t, e.q.Restore(ctx, jobID))
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

func TestInWindow(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 1, 1, h, m, 0, 0, time.UTC) }
	cases := []struct {
		now        time.Time
		start, end string
		want       bool
	}{
		{at(3, 0), "", "", true},
		{at(3, 0), "01:00", "07:00", true},
		{at(8, 0), "01:00", "07:00", false},
		{at(23, 30), "22:00", "06:00", true},
		{at(5, 59), "22:00", "06:00", true},
		{at(6, 0), "22:00", "06:00", false},
		{at(12, 0), "bad", "07:00", true},
	}
	for _, c := range cases {
		if got := InWindow(c.now, c.start, c.end); got != c.want {
			t.Errorf("%v %s-%s: %v", c.now.Format("15:04"), c.start, c.end, got)
		}
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
	if err := e.q.Restore(ctx, jobID); err != ErrItemBusy {
		t.Fatalf("restore while busy: %v", err)
	}
	unlock()
	must(t, e.q.Restore(ctx, jobID))
	b, _ := os.ReadFile(e.alpha)
	if sha256.Sum256(b) != e.hash {
		t.Fatal("restore did not put the original back")
	}
}
