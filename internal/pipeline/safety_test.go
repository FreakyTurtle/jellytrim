package pipeline

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/freakyturtle/jellytrim/internal/ffmpeg"
	"github.com/freakyturtle/jellytrim/internal/fileid"
	"github.com/freakyturtle/jellytrim/internal/media"
	"github.com/freakyturtle/jellytrim/internal/plan"
	"github.com/freakyturtle/jellytrim/internal/store"
)

// --- fakes -------------------------------------------------------------------

// memJournal is an in-memory journal that can fail on a chosen step.
type memJournal struct {
	mu      sync.Mutex
	entries []store.JournalEntry
	failOn  string
}

func (m *memJournal) JournalRecord(_ context.Context, jobID int64, step, path, other string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if step == m.failOn {
		return 0, errors.New("journal write failed")
	}
	id := int64(len(m.entries) + 1)
	m.entries = append(m.entries, store.JournalEntry{ID: id, JobID: jobID, Step: step, Path: path, OtherPath: other})
	return id, nil
}

func (m *memJournal) JournalComplete(_ context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	m.entries[id-1].CompletedAt = &now
	return nil
}

func (m *memJournal) JournalEntries(_ context.Context, _ int64) ([]store.JournalEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]store.JournalEntry(nil), m.entries...), nil
}

// faultFS wraps the real filesystem and fails one operation on its nth call.
type faultFS struct {
	OSFS
	op    string
	nth   int
	err   error
	calls map[string]int
	free  uint64
}

func (f *faultFS) hit(op string) error {
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[op]++
	if op == f.op && f.calls[op] == max(f.nth, 1) {
		if f.err != nil {
			return f.err
		}
		return &fs.PathError{Op: op, Path: "injected", Err: syscall.EIO}
	}
	return nil
}

func (f *faultFS) Link(a, b string) error {
	if err := f.hit("link"); err != nil {
		return err
	}
	return f.OSFS.Link(a, b)
}

func (f *faultFS) Rename(a, b string) error {
	if err := f.hit("rename"); err != nil {
		return err
	}
	return f.OSFS.Rename(a, b)
}

func (f *faultFS) Remove(a string) error {
	if err := f.hit("remove"); err != nil {
		return err
	}
	return f.OSFS.Remove(a)
}

func (f *faultFS) Chmod(a string, m fs.FileMode) error {
	if err := f.hit("chmod"); err != nil {
		return err
	}
	return f.OSFS.Chmod(a, m)
}

func (f *faultFS) Chown(_ string, _, _ int) error {
	if err := f.hit("chown"); err != nil {
		return err
	}
	return nil // tests cannot chown; the real call is not needed
}

func (f *faultFS) SyncFile(a string) error {
	if err := f.hit("syncfile"); err != nil {
		return err
	}
	return f.OSFS.SyncFile(a)
}

func (f *faultFS) Free(dir string) (uint64, error) {
	if f.free > 0 {
		return f.free, nil
	}
	return f.OSFS.Free(dir)
}

// fakeRunner "encodes" by writing a smaller file and answers probes from a
// fixture, adding the JELLYTRIM tag to outputs.
type fakeRunner struct {
	fixture    []byte
	outSize    int64
	runErr     error
	probeErr   error
	decodeErr  error
	codec      string // overrides the output's video codec, to fail validation
	stderrTail string
	progress   []ffmpeg.Progress
	blockCtx   bool // wait for cancellation instead of finishing
	onRun      func(output string)
}

func (r *fakeRunner) Run(ctx context.Context, args []string, _ time.Duration, onProgress func(ffmpeg.Progress)) (ffmpeg.Result, error) {
	out := strings.TrimPrefix(args[len(args)-1], "file:")
	f, err := os.Create(out)
	if err != nil {
		return ffmpeg.Result{}, err
	}
	_ = f.Truncate(r.outSize)
	_ = f.Close()
	if r.onRun != nil {
		r.onRun(out)
	}
	for _, pr := range r.progress {
		if onProgress != nil {
			onProgress(pr)
		}
		if ctx.Err() != nil {
			return ffmpeg.Result{Last: pr}, ctx.Err()
		}
	}
	if r.blockCtx {
		<-ctx.Done()
		return ffmpeg.Result{}, ctx.Err()
	}
	if r.runErr != nil {
		return ffmpeg.Result{StderrTail: "Error while encoding"}, r.runErr
	}
	return ffmpeg.Result{Last: ffmpeg.Progress{Fraction: 1, Done: true, Speed: 2}, StderrTail: r.stderrTail}, nil
}

func (r *fakeRunner) Probe(_ context.Context, _ string) ([]byte, []byte, error) {
	if r.probeErr != nil {
		return nil, nil, r.probeErr
	}
	var d map[string]any
	_ = json.Unmarshal(r.fixture, &d)
	format := d["format"].(map[string]any)
	tags, _ := format["tags"].(map[string]any)
	if tags == nil {
		tags = map[string]any{}
	}
	tags["JELLYTRIM"] = "v1;enc=x265;q=high"
	format["tags"] = tags
	if r.codec != "" {
		d["streams"].([]any)[0].(map[string]any)["codec_name"] = r.codec
	}
	b, _ := json.Marshal(d)
	return b, nil, nil
}

func (r *fakeRunner) Decode(context.Context, string, bool, time.Duration) error { return r.decodeErr }

// --- helpers -----------------------------------------------------------------

const sourceSize = 10_000_000

type rig struct {
	t       *testing.T
	dir     string
	path    string
	hash    [32]byte
	fs      *faultFS
	journal *memJournal
	runner  *fakeRunner
	job     Job
	// fsFailBoth makes hard links and the backup rename fail.
	fsFailBoth bool
}

func newRig(t *testing.T) *rig {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "Film (2020).mkv")
	content := bytes.Repeat([]byte("original media "), sourceSize/15)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile(filepath.Join("..", "media", "testdata", "probe", "multi-audio-subs.json"))
	if err != nil {
		t.Fatal(err)
	}
	src, err := media.Parse(fixture, nil, int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	v, _ := src.MainVideo()
	info, err := fileid.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := filepath.EvalSymlinks(dir)
	r := &rig{t: t, dir: dir, path: path, hash: sha256.Sum256(content), fs: &faultFS{}, journal: &memJournal{},
		runner: &fakeRunner{fixture: fixture, outSize: int64(len(content)) / 2}}
	r.job = Job{
		ID: 42, Path: path, Source: src, Identity: info, Roots: []string{filepath.ToSlash(root)},
		Plan: plan.Plan{Codec: media.CodecH264, Width: v.Width, Height: v.Height, BitDepth: v.BitDepth,
			Container: media.Matroska, HDR: media.SDR, EstMax: int64(len(content)) / 2},
		Args: func(in, out string) ([]string, error) {
			return []string{"-nostdin", "-i", "file:" + in, "file:" + out}, nil
		},
		Encoder:          "Software (x265)",
		MinSavingPercent: 10,
	}
	return r
}

func (r *rig) pipeline() *Pipeline {
	return &Pipeline{FS: r.fs, Runner: r.runner, Journal: r.journal, Sleep: func(time.Duration) {}}
}

// assertOriginalIntact checks the original is byte-for-byte unchanged and no
// JellyTrim file is left beside it.
func (r *rig) assertOriginalIntact(res Result) {
	r.t.Helper()
	b, err := os.ReadFile(r.path)
	if err != nil {
		r.t.Fatalf("original missing after %s (%s): %v", res.Outcome, res.Summary, err)
	}
	if sha256.Sum256(b) != r.hash {
		r.t.Fatalf("original changed after %s (%s)", res.Outcome, res.Summary)
	}
	r.assertNoLeftovers()
}

func (r *rig) assertNoLeftovers() {
	r.t.Helper()
	entries, _ := os.ReadDir(r.dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".jellytrim-") {
			r.t.Fatalf("left behind %s", e.Name())
		}
	}
}

// --- tests -------------------------------------------------------------------

func TestSafetyHappyPathKeepsBackup(t *testing.T) {
	r := newRig(t)
	res := r.pipeline().Execute(context.Background(), r.job, Hooks{})
	if res.Outcome != Complete {
		t.Fatalf("outcome %s: %s %+v", res.Outcome, res.Summary, res.Diagnostics)
	}
	fi, _ := os.Stat(r.path)
	if fi.Size() != r.runner.outSize {
		t.Fatalf("new file size %d", fi.Size())
	}
	b, err := os.ReadFile(res.BackupPath)
	if err != nil || sha256.Sum256(b) != r.hash {
		t.Fatalf("backup does not hold the original: %v", err)
	}
	if fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode %o", fi.Mode().Perm())
	}
	steps := []string{}
	for _, e := range r.journal.entries {
		steps = append(steps, e.Step)
		if e.CompletedAt == nil {
			t.Errorf("step %s not completed", e.Step)
		}
	}
	if strings.Join(steps, ",") != "partial_created,backup_linked,replaced" {
		t.Fatalf("journal %v", steps)
	}
}

func TestSafetyFailureAtEveryStepLeavesOriginal(t *testing.T) {
	cases := []struct {
		name  string
		setup func(r *rig)
		want  Outcome
	}{
		{"encoder error", func(r *rig) { r.runner.runErr = &ffmpeg.ExitError{Code: 1} }, Failed},
		{"probe of output fails", func(r *rig) { r.runner.probeErr = errors.New("moov atom not found") }, Failed},
		{"output has wrong codec", func(r *rig) { r.runner.codec = "mpeg4" }, Failed},
		{"output does not decode", func(r *rig) { r.runner.decodeErr = errors.New("corrupt frame") }, Failed},
		{"saving below minimum", func(r *rig) { r.runner.outSize = sourceSize * 95 / 100 }, Skipped},
		{"chmod fails", func(r *rig) { r.fs.op = "chmod" }, Failed},
		{"fsync fails", func(r *rig) { r.fs.op = "syncfile" }, Failed},
		{"link and rename-backup both fail", func(r *rig) { r.fsFailBoth = true }, Failed},
		{"final rename fails (linked backup)", func(r *rig) { r.fs.op, r.fs.nth = "rename", 1 }, Failed},
		{"final rename crosses devices", func(r *rig) { r.fs.op, r.fs.nth, r.fs.err = "rename", 1, syscall.EXDEV }, Failed},
		{"journal fails before encoding", func(r *rig) { r.journal.failOn = StepPartialCreated }, Failed},
		{"journal fails before backup", func(r *rig) { r.journal.failOn = StepBackupLinked }, Failed},
		{"journal fails before replace", func(r *rig) { r.journal.failOn = StepReplaced }, Failed},
		{"original changes while encoding", func(r *rig) {
			r.runner.onRun = func(string) {
				_ = os.WriteFile(r.path, []byte("someone else replaced this"), 0o644)
				r.hash = sha256.Sum256([]byte("someone else replaced this"))
			}
		}, Skipped},
		{"original gains a hard link while encoding", func(r *rig) {
			r.runner.onRun = func(string) { _ = os.Link(r.path, filepath.Join(r.dir, "seed.mkv")) }
		}, Skipped},
		{"not enough free space", func(r *rig) { r.fs.free = 1 }, Skipped},
		{"early abort when output is too big", func(r *rig) {
			r.runner.progress = []ffmpeg.Progress{{Fraction: 0.2, OutTime: 20 * time.Minute, TotalSize: sourceSize / 5}}
		}, Skipped},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			tc.setup(r)
			p := r.pipeline()
			if r.fsFailBoth {
				p.FS = &linkAndRenameFail{faultFS: r.fs}
			}
			res := p.Execute(context.Background(), r.job, Hooks{})
			if res.Outcome != tc.want {
				t.Fatalf("outcome %s, want %s (%s)", res.Outcome, tc.want, res.Summary)
			}
			if !strings.Contains(res.Summary, "unchanged") && !strings.Contains(res.Summary, "left it alone") &&
				!strings.Contains(res.Summary, "free space") {
				t.Errorf("summary should reassure the original is intact: %q", res.Summary)
			}
			r.assertOriginalIntact(res)
		})
	}
}

// linkAndRenameFail fails every link and the first rename (the backup).
type linkAndRenameFail struct{ *faultFS }

func (l *linkAndRenameFail) Link(string, string) error { return syscall.EPERM }

func (l *linkAndRenameFail) Rename(a, b string) error {
	if strings.Contains(b, ".jellytrim-bak-") {
		return syscall.EACCES
	}
	return l.OSFS.Rename(a, b)
}

func TestSafetyRenameBackupFallback(t *testing.T) {
	r := newRig(t)
	p := r.pipeline()
	p.FS = &noLinks{faultFS: r.fs}
	res := p.Execute(context.Background(), r.job, Hooks{})
	if res.Outcome != Complete {
		t.Fatalf("outcome %s: %s", res.Outcome, res.Summary)
	}
	b, err := os.ReadFile(res.BackupPath)
	if err != nil || sha256.Sum256(b) != r.hash {
		t.Fatalf("rename-based backup does not hold the original: %v", err)
	}
}

// noLinks simulates a filesystem without hard links (SMB, some FUSE).
type noLinks struct{ *faultFS }

func (noLinks) Link(string, string) error { return syscall.ENOTSUP }

func TestSafetyFinalRenameFailsAfterRenameBackupRestoresOriginal(t *testing.T) {
	r := newRig(t)
	p := r.pipeline()
	p.FS = &noLinksFailSecondRename{faultFS: r.fs}
	res := p.Execute(context.Background(), r.job, Hooks{})
	if res.Outcome != Failed {
		t.Fatalf("outcome %s", res.Outcome)
	}
	r.assertOriginalIntact(res)
}

type noLinksFailSecondRename struct {
	*faultFS
	n int
}

func (f *noLinksFailSecondRename) Link(string, string) error { return syscall.ENOTSUP }

func (f *noLinksFailSecondRename) Rename(a, b string) error {
	f.n++
	if f.n == 2 { // 1: original to backup, 2: partial to original
		return syscall.EIO
	}
	return f.OSFS.Rename(a, b)
}

func TestSafetyPreflightSkips(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		r := newRig(t)
		link := filepath.Join(r.dir, "link.mkv")
		if err := os.Symlink(r.path, link); err != nil {
			t.Fatal(err)
		}
		r.job.Path = link
		r.job.Identity, _ = fileid.Stat(link)
		res := r.pipeline().Execute(context.Background(), r.job, Hooks{})
		if res.Outcome != Skipped || !strings.Contains(res.Summary, "symbolic link") {
			t.Fatalf("%s: %s", res.Outcome, res.Summary)
		}
		r.assertOriginalIntact(res)
	})
	t.Run("outside roots", func(t *testing.T) {
		r := newRig(t)
		r.job.Roots = []string{"/somewhere/else"}
		res := r.pipeline().Execute(context.Background(), r.job, Hooks{})
		if res.Outcome != Skipped || !strings.Contains(res.Summary, "outside") {
			t.Fatalf("%s: %s", res.Outcome, res.Summary)
		}
		r.assertOriginalIntact(res)
	})
	t.Run("hard link", func(t *testing.T) {
		r := newRig(t)
		if err := os.Link(r.path, filepath.Join(r.dir, "seed.mkv")); err != nil {
			t.Fatal(err)
		}
		r.job.Identity, _ = fileid.Stat(r.path)
		res := r.pipeline().Execute(context.Background(), r.job, Hooks{})
		if res.Outcome != Skipped || !strings.Contains(res.Summary, "hard link") {
			t.Fatalf("%s: %s", res.Outcome, res.Summary)
		}
	})
	t.Run("leftover partial is never overwritten", func(t *testing.T) {
		r := newRig(t)
		leftover := PartialPath(r.path, r.job.ID)
		if err := os.WriteFile(leftover, []byte("from a crash"), 0o644); err != nil {
			t.Fatal(err)
		}
		res := r.pipeline().Execute(context.Background(), r.job, Hooks{})
		if res.Outcome != Failed {
			t.Fatalf("%s", res.Outcome)
		}
		if b, _ := os.ReadFile(leftover); string(b) != "from a crash" {
			t.Fatal("leftover partial was touched")
		}
	})
}

func TestSafetyCancelDuringEncode(t *testing.T) {
	r := newRig(t)
	r.runner.blockCtx = true
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan Result)
	go func() { done <- r.pipeline().Execute(ctx, r.job, Hooks{}) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	res := <-done
	if res.Outcome != Interrupted {
		t.Fatalf("outcome %s", res.Outcome)
	}
	r.assertOriginalIntact(res)
}

// --- recovery ----------------------------------------------------------------

func TestRecoveryStates(t *testing.T) {
	ctx := context.Background()
	t.Run("partial left by a crash is removed", func(t *testing.T) {
		r := newRig(t)
		partial := PartialPath(r.path, 42)
		_ = os.WriteFile(partial, []byte("half"), 0o644)
		_, _ = r.journal.JournalRecord(ctx, 42, StepPartialCreated, partial, r.path)
		rec, err := r.pipeline().Recover(ctx, 42, r.path)
		if err != nil || rec.State != RecoveredCleaned {
			t.Fatalf("%+v %v", rec, err)
		}
		r.assertOriginalIntact(Result{})
	})
	t.Run("crash after linking the backup", func(t *testing.T) {
		r := newRig(t)
		partial, backup := PartialPath(r.path, 42), BackupPath(r.path, 42)
		_ = os.WriteFile(partial, []byte("encoded"), 0o644)
		_ = os.Link(r.path, backup)
		_, _ = r.journal.JournalRecord(ctx, 42, StepPartialCreated, partial, r.path)
		_, _ = r.journal.JournalRecord(ctx, 42, StepBackupLinked, backup, r.path)
		rec, err := r.pipeline().Recover(ctx, 42, r.path)
		if err != nil || rec.State != RecoveredCleaned {
			t.Fatalf("%+v %v", rec, err)
		}
		r.assertOriginalIntact(Result{})
	})
	t.Run("crash after renaming the original to the backup", func(t *testing.T) {
		r := newRig(t)
		partial, backup := PartialPath(r.path, 42), BackupPath(r.path, 42)
		_ = os.WriteFile(partial, []byte("encoded"), 0o644)
		_ = os.Rename(r.path, backup)
		_, _ = r.journal.JournalRecord(ctx, 42, StepPartialCreated, partial, r.path)
		_, _ = r.journal.JournalRecord(ctx, 42, StepBackupRenamed, backup, r.path)
		_, _ = r.journal.JournalRecord(ctx, 42, StepReplaced, r.path, partial)
		rec, err := r.pipeline().Recover(ctx, 42, r.path)
		if err != nil || rec.State != RecoveredRestored {
			t.Fatalf("%+v %v", rec, err)
		}
		r.assertOriginalIntact(Result{})
	})
	t.Run("crash just after the final rename", func(t *testing.T) {
		r := newRig(t)
		partial, backup := PartialPath(r.path, 42), BackupPath(r.path, 42)
		_ = os.Link(r.path, backup)
		_ = os.WriteFile(partial, []byte("encoded"), 0o644)
		_ = os.Rename(partial, r.path)
		_, _ = r.journal.JournalRecord(ctx, 42, StepPartialCreated, partial, r.path)
		_, _ = r.journal.JournalRecord(ctx, 42, StepBackupLinked, backup, r.path)
		_, _ = r.journal.JournalRecord(ctx, 42, StepReplaced, r.path, partial) // never marked complete
		rec, err := r.pipeline().Recover(ctx, 42, r.path)
		if err != nil || rec.State != RecoveredReplaced || rec.BackupPath != backup {
			t.Fatalf("%+v %v", rec, err)
		}
		b, _ := os.ReadFile(backup)
		if sha256.Sum256(b) != r.hash {
			t.Fatal("backup must still hold the original")
		}
	})
	t.Run("journal paths outside the folder are refused", func(t *testing.T) {
		r := newRig(t)
		victim := filepath.Join(t.TempDir(), "important.mkv")
		_ = os.WriteFile(victim, []byte("keep me"), 0o644)
		_, _ = r.journal.JournalRecord(ctx, 42, StepPartialCreated, victim, r.path)
		if _, err := r.pipeline().Recover(ctx, 42, r.path); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("err %v", err)
		}
		if b, _ := os.ReadFile(victim); string(b) != "keep me" {
			t.Fatal("recovery touched a file outside its own names")
		}
	})
}

func TestRestoreAndDeleteBackup(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	p := r.pipeline()
	res := p.Execute(ctx, r.job, Hooks{})
	if res.Outcome != Complete {
		t.Fatalf("%s", res.Summary)
	}
	if err := p.Restore(ctx, 42, res.BackupPath, r.path, res.Output); err != nil {
		t.Fatal(err)
	}
	r.assertOriginalIntact(res)
	if err := p.DeleteBackup(ctx, 42, "/etc/passwd", r.path); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("delete outside: %v", err)
	}
}
