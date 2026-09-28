// Package pipeline runs one optimisation safely: pre-flight checks, encode
// to a hidden partial file next to the original, validate the result, then
// replace the original atomically while keeping it as a backup. Every
// filesystem step is journalled before it happens so a crash can always be
// recovered from. See docs/TRANSCODING.md sections 5 to 7.
//
// The rule behind every branch: the original is never changed unless every
// check has passed, and any doubt leaves it exactly as it was.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/freakyturtle/jellytrim/internal/ffmpeg"
	"github.com/freakyturtle/jellytrim/internal/fileid"
	"github.com/freakyturtle/jellytrim/internal/media"
	"github.com/freakyturtle/jellytrim/internal/pathmap"
	"github.com/freakyturtle/jellytrim/internal/plan"
	"github.com/freakyturtle/jellytrim/internal/store"
	"github.com/freakyturtle/jellytrim/internal/units"
)

// Journal steps.
const (
	StepPartialCreated = "partial_created"
	StepPartialDeleted = "partial_deleted"
	StepBackupLinked   = "backup_linked"
	StepBackupRenamed  = "backup_renamed"
	StepReplaced       = "replaced"
	StepUndone         = "undone"
	StepBackupDeleted  = "backup_deleted"
	StepRestored       = "restored"
)

// Runner runs ffmpeg and ffprobe. Implemented by *ffmpeg.Runner.
type Runner interface {
	Run(ctx context.Context, args []string, total time.Duration, onProgress func(ffmpeg.Progress)) (ffmpeg.Result, error)
	Probe(ctx context.Context, path string) (probeJSON, frameJSON []byte, err error)
	Decode(ctx context.Context, path string, sampled bool, duration time.Duration) error
}

// Journal persists intended filesystem steps. Implemented by *store.Store.
type Journal interface {
	JournalRecord(ctx context.Context, jobID int64, step, path, other string) (int64, error)
	JournalComplete(ctx context.Context, entryID int64) error
	JournalEntries(ctx context.Context, jobID int64) ([]store.JournalEntry, error)
}

// Outcome is how a run ended.
type Outcome string

// Outcomes.
const (
	Complete Outcome = "complete"
	// Skipped means JellyTrim chose not to replace the file (too little
	// saving, the file changed, not enough space). The original is intact.
	Skipped Outcome = "skipped"
	// Failed means something went wrong. The original is intact.
	Failed Outcome = "failed"
	// Interrupted means the run was stopped (shutdown or cancel) before the
	// replacement started. The original is intact and the job can run again.
	Interrupted Outcome = "interrupted"
	// NeedsAttention means the original is safe but not at its usual path
	// (it is at BackupPath). Recovery retries on every start.
	NeedsAttention Outcome = "attention"
)

// ArgsFunc builds the ffmpeg arguments to encode input into output.
type ArgsFunc func(input, output string) ([]string, error)

// Job is one file to optimise.
type Job struct {
	ID       int64
	Path     string      // the original, as JellyTrim sees it
	Plan     plan.Plan   // what to produce
	Source   *media.File // the original's probe, taken just before starting
	Identity fileid.Info // the original's identity when the job started
	Args     ArgsFunc
	Encoder  string   // backend label for messages, "Intel Quick Sync (HEVC)"
	Roots    []string // resolved local roots JellyTrim may change files in

	MinSavingPercent int
	SampledDecode    bool
}

// Hooks report progress to the queue. Either may be nil.
type Hooks struct {
	Status   func(status string)
	Progress func(ffmpeg.Progress)
}

func (h Hooks) status(s string) {
	if h.Status != nil {
		h.Status(s)
	}
}

// Check is one validation check and its result.
type Check struct {
	Name   string `json:"name"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail"`
}

// Diagnostics are the technical details kept for History.
type Diagnostics struct {
	Step       string   `json:"step"`
	Encoder    string   `json:"encoder,omitempty"`
	Args       []string `json:"args,omitempty"`
	StderrTail string   `json:"stderr_tail,omitempty"`
	Error      string   `json:"error,omitempty"`
	Checks     []Check  `json:"checks,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`
	Speed      float64  `json:"speed,omitempty"`
	EncodeTime string   `json:"encode_time,omitempty"`
}

// Result is the outcome of Execute.
type Result struct {
	Outcome     Outcome
	Summary     string // one or two plain sentences for the UI
	Diagnostics Diagnostics
	OutputSize  int64
	Output      fileid.Info // the new file, when Complete
	BackupPath  string      // where the original was kept, when Complete
}

// Pipeline executes jobs.
type Pipeline struct {
	FS      FS
	Runner  Runner
	Journal Journal
	Log     *slog.Logger
	// Sleep is used between retries of a busy rename; replaced in tests.
	Sleep func(time.Duration)
}

// PartialPath is the hidden file an encode writes to, next to the original.
func PartialPath(path string, jobID int64) string {
	return filepath.Join(filepath.Dir(path), fmt.Sprintf(".%s.jellytrim-%d.partial", filepath.Base(path), jobID))
}

// BackupPath is the hidden name the original is kept under after replacement.
func BackupPath(path string, jobID int64) string {
	return filepath.Join(filepath.Dir(path), fmt.Sprintf(".%s.jellytrim-bak-%d", filepath.Base(path), jobID))
}

// Execute runs a job to completion or to a safe stop.
func (p *Pipeline) Execute(ctx context.Context, j Job, h Hooks) Result {
	diag := Diagnostics{Step: "analysing", Encoder: j.Encoder}
	if r, ok := p.preflight(j, diag); !ok {
		return r
	}
	partial := PartialPath(j.Path, j.ID)
	h.status(store.JobEncoding)
	res, ok := p.encode(ctx, j, partial, h, &diag)
	if !ok {
		return res
	}
	h.status(store.JobValidating)
	out, res, ok := p.validate(ctx, j, partial, &diag)
	if !ok {
		return res
	}
	h.status(store.JobReplacing)
	return p.replace(ctx, j, partial, out, diag)
}

func (p *Pipeline) log() *slog.Logger {
	if p.Log == nil {
		return slog.Default()
	}
	return p.Log
}

// preflight re-checks everything that could have changed since the item was
// evaluated. Nothing on disk is touched.
func (p *Pipeline) preflight(j Job, diag Diagnostics) (Result, bool) {
	skip := func(msg string) (Result, bool) {
		return Result{Outcome: Skipped, Summary: msg, Diagnostics: diag}, false
	}
	if j.Source == nil || j.Args == nil || j.Plan.Width == 0 {
		return Result{Outcome: Failed, Summary: "The job is incomplete, so nothing was changed.", Diagnostics: diag}, false
	}
	cur, err := p.FS.Stat(j.Path)
	switch {
	case err == nil && j.Source.Size > 0 && j.Source.Size != cur.Size:
		return skip("The file changed since it was inspected, so JellyTrim left it alone.")
	case err != nil:
		return skip("The file could not be read: " + err.Error())
	case !cur.Same(j.Identity):
		return skip("The file changed since the job started, so JellyTrim left it alone.")
	case cur.IsSymlink:
		return skip("The file is a symbolic link. JellyTrim only replaces real files.")
	case cur.Nlink > 1:
		return skip("The file has more than one hard link. Replacing it would use more space, not less.")
	}
	resolved, err := filepath.EvalSymlinks(j.Path)
	if err != nil || !pathmap.Within(j.Roots, filepath.ToSlash(resolved)) {
		return skip("The file is outside the folders JellyTrim is allowed to change.")
	}
	free, err := p.FS.Free(filepath.Dir(j.Path))
	if err != nil {
		return skip("JellyTrim could not check the free space in the file's folder, so it did not start.")
	}
	need := uint64(max(j.Plan.EstMax, 0)) + uint64(max(j.Plan.EstMax/5, 0)) + 64<<20
	if free < need {
		return skip(fmt.Sprintf("Not enough free space: the new file needs about %s and %s is free.",
			units.Bytes(int64(need)), units.Bytes(int64(min(free, 1<<62)))))
	}
	return Result{}, true
}

// errEarlyAbort marks an encode stopped because it would not save enough.
var errEarlyAbort = errors.New("stopped early: the output would not be small enough")
