package pipeline

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/freakyturtle/jellytrim/internal/media"
	"github.com/freakyturtle/jellytrim/internal/plan"
)

// Cases from the safety review: double failures, renames that succeed but
// report an error, files replaced by other programs, and Restore misuse.

// noLinksFailAll: no hard links, the final rename fails, and so does every
// attempt to rename the backup back.
type noLinksFailAll struct{ *faultFS }

func (noLinksFailAll) Link(string, string) error { return syscall.ENOTSUP }

func (f noLinksFailAll) Rename(a, b string) error {
	if strings.Contains(b, ".jellytrim-bak-") {
		return f.OSFS.Rename(a, b) // taking the backup works
	}
	return syscall.EIO
}

func TestSafetyDoubleFailureNeverClaimsUnchanged(t *testing.T) {
	r := newRig(t)
	p := r.pipeline()
	p.FS = noLinksFailAll{faultFS: r.fs}
	res := p.Execute(context.Background(), r.job, Hooks{})
	if res.Outcome != NeedsAttention {
		t.Fatalf("outcome %s: %s", res.Outcome, res.Summary)
	}
	if strings.Contains(res.Summary, "unchanged") || !strings.Contains(res.Summary, res.BackupPath) {
		t.Fatalf("summary must say where the original is: %q", res.Summary)
	}
	b, err := os.ReadFile(res.BackupPath)
	if err != nil || sha256.Sum256(b) != r.hash {
		t.Fatalf("the original must be intact at the backup path: %v", err)
	}

	// On the next start, recovery puts it back.
	rp := r.pipeline()
	rec, err := rp.Recover(context.Background(), r.job.ID, r.path)
	if err != nil || rec.State != RecoveredRestored {
		t.Fatalf("%+v %v", rec, err)
	}
	r.assertOriginalIntact(res)
}

// ambiguousRename performs the final rename but reports an error, like a
// network filesystem that lost the reply.
type ambiguousRename struct{ *faultFS }

func (a ambiguousRename) Rename(from, to string) error {
	err := a.OSFS.Rename(from, to)
	if err == nil && strings.HasSuffix(from, ".partial") {
		return syscall.EIO
	}
	return err
}

func TestSafetyRenameThatSucceededIsComplete(t *testing.T) {
	r := newRig(t)
	p := r.pipeline()
	p.FS = ambiguousRename{faultFS: r.fs}
	res := p.Execute(context.Background(), r.job, Hooks{})
	if res.Outcome != Complete {
		t.Fatalf("outcome %s: %s", res.Outcome, res.Summary)
	}
	b, _ := os.ReadFile(res.BackupPath)
	if sha256.Sum256(b) != r.hash {
		t.Fatal("backup must hold the original")
	}
}

// swapAfterLink replaces the file at the path right after the backup link,
// as an *arr import of an upgrade might.
type swapAfterLink struct {
	*faultFS
	path string
}

func (s swapAfterLink) Link(a, b string) error {
	if err := s.OSFS.Link(a, b); err != nil {
		return err
	}
	tmp := s.path + ".upgrade"
	_ = os.WriteFile(tmp, []byte("an upgrade from another program"), 0o644)
	return os.Rename(tmp, s.path)
}

func TestSafetyFileReplacedByAnotherProgramIsLeftAlone(t *testing.T) {
	r := newRig(t)
	p := r.pipeline()
	p.FS = swapAfterLink{faultFS: r.fs, path: r.path}
	res := p.Execute(context.Background(), r.job, Hooks{})
	if res.Outcome != Skipped {
		t.Fatalf("outcome %s: %s", res.Outcome, res.Summary)
	}
	if b, _ := os.ReadFile(r.path); string(b) != "an upgrade from another program" {
		t.Fatal("the other program's file was overwritten")
	}
	if b, _ := os.ReadFile(res.BackupPath); sha256.Sum256(b) != r.hash {
		t.Fatal("the version JellyTrim started from must be kept")
	}
}

func TestSafetyEncoderErrorsOnCleanExit(t *testing.T) {
	r := newRig(t)
	r.runner.stderrTail = "[h264 @ 0x1] error while decoding MB 12 34"
	res := r.pipeline().Execute(context.Background(), r.job, Hooks{})
	if res.Outcome != Failed || !strings.Contains(res.Summary, "reported errors") {
		t.Fatalf("%s: %s", res.Outcome, res.Summary)
	}
	r.assertOriginalIntact(res)
}

func TestRestoreRefusesANewerFile(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	p := r.pipeline()
	res := p.Execute(ctx, r.job, Hooks{})
	if res.Outcome != Complete {
		t.Fatal(res.Summary)
	}
	// A new download replaces the optimised file.
	if err := os.WriteFile(r.path+".new", []byte("new download"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(r.path+".new", r.path); err != nil {
		t.Fatal(err)
	}
	if err := p.Restore(ctx, r.job.ID, res.BackupPath, r.path, res.Output); !errors.Is(err, ErrNotOurFile) {
		t.Fatalf("err %v", err)
	}
	if b, _ := os.ReadFile(r.path); string(b) != "new download" {
		t.Fatal("restore overwrote a file JellyTrim did not produce")
	}
}

func TestRecoveryDoesNotMistakeAnUndoneReplace(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	partial, backup := PartialPath(r.path, 42), BackupPath(r.path, 42)
	// Link backup made; the replace was recorded but never happened; the
	// partial was then removed without a journal entry (database failure).
	_ = os.Link(r.path, backup)
	_, _ = r.journal.JournalRecord(ctx, 42, StepPartialCreated, partial, r.path)
	_, _ = r.journal.JournalRecord(ctx, 42, StepBackupLinked, backup, r.path)
	_, _ = r.journal.JournalRecord(ctx, 42, StepReplaced, r.path, partial)
	rec, err := r.pipeline().Recover(ctx, 42, r.path)
	if err != nil || rec.State == RecoveredReplaced {
		t.Fatalf("an unchanged original was taken for a replacement: %+v %v", rec, err)
	}
	r.assertOriginalIntact(Result{})
}

func TestRecoveryRefusesOtherJobsNames(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	other := BackupPath(r.path, 7) // a real JellyTrim name, but another job's
	_ = os.WriteFile(other, []byte("job 7's backup"), 0o644)
	_, _ = r.journal.JournalRecord(ctx, 42, StepPartialCreated, other, r.path)
	if _, err := r.pipeline().Recover(ctx, 42, r.path); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("err %v", err)
	}
	if b, _ := os.ReadFile(other); string(b) != "job 7's backup" {
		t.Fatal("recovery touched another job's file")
	}
}

func TestValidateRejectsEveryKindOfLoss(t *testing.T) {
	load := func(name string) *media.File {
		b, err := os.ReadFile(filepath.Join("..", "media", "testdata", "probe", name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		fr, _ := os.ReadFile(filepath.Join("..", "media", "testdata", "probe", name+".frames.json"))
		f, err := media.Parse(b, fr, 0)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	src := load("multi-audio-subs")
	v, _ := src.MainVideo()
	pl := plan.Plan{Codec: v.Codec, Width: v.Width, Height: v.Height, BitDepth: v.BitDepth, Container: src.Container, HDR: media.SDR}
	good := func() *media.File {
		o := load("multi-audio-subs")
		o.Tags = map[string]string{"JELLYTRIM": "v1"}
		o.Size = src.Size / 2
		return o
	}
	failing := func(cs []Check) []string {
		var names []string
		for _, c := range cs {
			if !c.Pass {
				names = append(names, c.Name)
			}
		}
		return names
	}
	if f := failing(Validate(src, good(), pl, 10)); len(f) != 0 {
		t.Fatalf("a faithful output failed: %v", f)
	}
	cases := map[string]func(o *media.File){
		"audio_count":    func(o *media.File) { o.Audio = o.Audio[:2] },
		"audio_2":        func(o *media.File) { o.Audio[1].Language = "fre" },
		"subtitle_2":     func(o *media.File) { o.Subtitles[1].Disposition.Forced = false },
		"subtitle_count": func(o *media.File) { o.Subtitles = nil },
		"chapters":       func(o *media.File) { o.Chapters++ },
		"duration":       func(o *media.File) { o.Duration += 10e9 },
		"size":           func(o *media.File) { o.Video[0].Width /= 2 },
		"saving":         func(o *media.File) { o.Size = src.Size },
		"container":      func(o *media.File) { o.Container = media.MP4 },
		"marker":         func(o *media.File) { o.Tags = nil },
		"cover_art": func(o *media.File) {
			o.Video = append(o.Video, media.VideoStream{Codec: "png", Disposition: media.Disposition{AttachedPic: true}})
		},
		"hdr": func(o *media.File) {
			o.Video[0].Transfer, o.Video[0].Primaries = "smpte2084", "bt2020"
		},
	}
	for want, mutate := range cases {
		o := good()
		mutate(o)
		got := failing(Validate(src, o, pl, 10))
		found := false
		for _, g := range got {
			if g == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: expected that check to fail, failing checks were %v", want, got)
		}
	}
}

func TestStopDuringValidationIsAnInterruption(t *testing.T) {
	r := newRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	r.runner.onRun = func(string) { cancel() } // stopped by the schedule mid-job
	r.runner.decodeErr = context.Canceled
	res := r.pipeline().Execute(ctx, r.job, Hooks{})
	if res.Outcome != Interrupted {
		t.Fatalf("outcome %s: %s", res.Outcome, res.Summary)
	}
	r.assertOriginalIntact(res)
}
