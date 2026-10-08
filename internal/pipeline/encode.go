package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/freakyturtle/jellytrim/internal/ffmpeg"
	"github.com/freakyturtle/jellytrim/internal/units"
)

// Early abort: once this fraction of the file, and at least abortMinTime of
// it, is encoded, stop if the output is on course to be larger than
// abortRatio of the original. The minimum time stops headers and the first
// keyframes (which cost far more than average) skewing the projection.
const (
	abortAfter   = 0.15
	abortMinTime = 2 * time.Minute
	abortRatio   = 0.90
)

// encode writes the partial file. On any failure the partial file is
// removed and ok is false.
func (p *Pipeline) encode(ctx context.Context, j Job, partial string, h Hooks, diag *Diagnostics) (Result, bool) {
	diag.Step = "encoding"
	if p.FS.Exists(partial) {
		// A leftover from a crash is only ever removed through recovery,
		// which checks the journal. Refuse rather than overwrite.
		return Result{Outcome: Failed, Summary: "A partial file from an earlier attempt is still present. Restart JellyTrim so it can clean up safely.", Diagnostics: *diag}, false
	}
	args, err := j.Args(j.Path, partial)
	if err != nil {
		diag.Error = err.Error()
		return Result{Outcome: Failed, Summary: "JellyTrim could not prepare the encoder settings: " + err.Error(), Diagnostics: *diag}, false
	}
	diag.Args = args
	entry, err := p.Journal.JournalRecord(ctx, j.ID, StepPartialCreated, partial, j.Path)
	if err != nil {
		diag.Error = err.Error()
		return Result{Outcome: Failed, Summary: "JellyTrim could not write its journal, so it did not start. The original is unchanged.", Diagnostics: *diag}, false
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	var aborted bool
	var last ffmpeg.Progress
	onProgress := func(pr ffmpeg.Progress) {
		mu.Lock()
		last = pr
		if !aborted && pr.Fraction >= abortAfter && pr.OutTime >= abortMinTime && pr.TotalSize > 0 && j.Source.Size > 0 &&
			float64(pr.TotalSize)/pr.Fraction > abortRatio*float64(j.Source.Size) {
			aborted = true
			cancel()
		}
		mu.Unlock()
		if h.Progress != nil {
			h.Progress(pr)
		}
	}
	started := time.Now()
	res, runErr := p.Runner.Run(runCtx, args, j.Source.Duration, onProgress)
	diag.StderrTail = res.StderrTail
	diag.EncodeTime = units.Duration(time.Since(started))
	diag.Speed = res.Last.Speed
	_ = p.Journal.JournalComplete(ctx, entry)

	mu.Lock()
	wasAborted := aborted
	lastProgress := last
	mu.Unlock()
	if runErr == nil && !wasAborted && ffmpeg.ReportedErrors(res.StderrTail) == "" {
		return Result{}, true
	}
	if runErr == nil && !wasAborted {
		// ffmpeg logs at error level only when something went wrong, for
		// example frames it could not decode. A clean exit code alone is not
		// enough to trust the output.
		runErr = errEncoderReported
	}
	p.removePartial(context.WithoutCancel(ctx), j.ID, partial, diag)
	return encodeFailure(ctx, j, runErr, wasAborted, lastProgress, diag), false
}

func encodeFailure(ctx context.Context, j Job, runErr error, aborted bool, last ffmpeg.Progress, diag *Diagnostics) Result {
	switch {
	case aborted:
		projected := int64(float64(last.TotalSize) / last.Fraction)
		diag.Error = errEarlyAbort.Error()
		return Result{Outcome: Skipped, Diagnostics: *diag, Summary: fmt.Sprintf(
			"The new file was on course to be about %s, close to the original's %s, so JellyTrim stopped early. The original is unchanged.",
			units.Bytes(projected), units.Bytes(j.Source.Size))}
	case ctx.Err() != nil:
		diag.Error = "stopped: " + ctx.Err().Error()
		return Result{Outcome: Interrupted, Diagnostics: *diag, Summary: "Stopped before finishing. The original is unchanged."}
	}
	diag.Error = runErr.Error()
	if errors.Is(runErr, errEncoderReported) {
		return Result{Outcome: Failed, Diagnostics: *diag,
			Summary: "ffmpeg reported errors while encoding (see the technical details), so the new file was discarded. The original is unchanged."}
	}
	pct := int(last.Fraction * 100)
	who := j.Encoder
	if who == "" {
		who = "The encoder"
	}
	summary := fmt.Sprintf("Encoding failed at %d%%. %s returned an error. The original is unchanged.", pct, who)
	var exitErr *ffmpeg.ExitError
	if !errors.As(runErr, &exitErr) {
		summary = "Encoding could not start: " + firstLine(runErr.Error()) + ". The original is unchanged."
	}
	return Result{Outcome: Failed, Diagnostics: *diag, Summary: summary}
}

// errEncoderReported marks an encode that exited cleanly but logged errors.
var errEncoderReported = errors.New("ffmpeg reported errors while encoding")

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// removePartial deletes the job's own partial file, journalling it. It only
// ever removes the exact path the job created. A failure is kept as a
// warning; callers that must not carry on after one use tryRemovePartial.
func (p *Pipeline) removePartial(ctx context.Context, jobID int64, partial string, diag *Diagnostics) {
	_ = p.tryRemovePartial(ctx, jobID, partial, diag)
}

// tryRemovePartial is removePartial, returning the error when the file is
// still there afterwards.
func (p *Pipeline) tryRemovePartial(ctx context.Context, jobID int64, partial string, diag *Diagnostics) error {
	if !p.FS.Exists(partial) {
		return nil
	}
	entry, err := p.Journal.JournalRecord(ctx, jobID, StepPartialDeleted, partial, "")
	if err != nil {
		diag.Warnings = append(diag.Warnings, "Could not journal removing the partial file: "+err.Error())
	}
	if err := p.FS.Remove(partial); err != nil {
		diag.Warnings = append(diag.Warnings, "Could not remove the partial file "+partial+": "+err.Error())
		p.log().Warn("pipeline: removing partial", "path", partial, "err", err)
		return err
	}
	if entry != 0 {
		_ = p.Journal.JournalComplete(ctx, entry)
	}
	return nil
}
