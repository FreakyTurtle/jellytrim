package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/freakyturtle/jellytrim/internal/encoder"
	"github.com/freakyturtle/jellytrim/internal/fileid"
	"github.com/freakyturtle/jellytrim/internal/media"
	"github.com/freakyturtle/jellytrim/internal/pipeline"
	"github.com/freakyturtle/jellytrim/internal/plan"
	"github.com/freakyturtle/jellytrim/internal/store"
	"github.com/freakyturtle/jellytrim/internal/units"
)

// Past jobs are written through the same store methods the queue uses,
// with the store's clock set to when each step happened. The files they
// "replaced" are resized in place (still sparse), so the Library shows the
// new, smaller file, and each is recorded as optimised as the queue does.

// Typical software x265 speeds (1.0 is real time), when the table gives none.
const (
	filmSpeed    = 0.65
	episodeSpeed = 2.2
)

// restoredReason matches what the queue records when a user restores.
const restoredReason = "You restored the original, so JellyTrim leaves this item alone until you allow it again."

// at sets the store's clock, so rows are dated as if written then.
func (d *demo) at(t time.Time) { d.st.SetClock(func() time.Time { return t }) }

// seedHistory writes the History table's jobs, oldest first. Queueing
// refuses in Dry Run, so Dry Run is off while it runs.
func (d *demo) seedHistory(ctx context.Context) error {
	defer d.st.SetClock(d.now)
	settings, err := d.st.Settings(ctx)
	if err != nil {
		return err
	}
	for _, h := range history {
		if err := d.pastJob(ctx, h, settings); err != nil {
			return fmt.Errorf("history job for %q: %w", h.Item, err)
		}
	}
	return nil
}

func (d *demo) pastJob(ctx context.Context, h pastJob, settings store.Settings) error {
	e := d.cat.byKey[h.Item]
	now := d.now()
	finish := daysAgo(now, h.Days)
	speed := h.Speed
	if speed == 0 {
		speed = filmSpeed
		if e.Show != nil {
			speed = episodeSpeed
		}
	}
	took := time.Duration(float64(e.Duration) / speed)
	if h.Result == failed {
		took = took * 61 / 100
	}
	start := finish.Add(-took)
	d.at(start.Add(-time.Duration(20+jitter(h.Item+"q")*300) * time.Minute))
	jobID, _, err := d.queue.Enqueue(ctx, e.ID, "auto")
	if err != nil {
		return fmt.Errorf("queueing it (the policies must choose it): %w", err)
	}
	if h.Result == cancelled {
		d.at(finish)
		_, err := d.st.CancelJob(ctx, jobID)
		return err
	}
	job, err := d.st.Job(ctx, jobID)
	if err != nil {
		return err
	}
	var p plan.Plan
	if err := json.Unmarshal([]byte(job.Plan), &p); err != nil {
		return err
	}
	backend, ok := d.reg.Backend(p.Encoder)
	if !ok {
		return fmt.Errorf("no encoder %q", p.Encoder)
	}
	local := e.Local(d.dir.media())
	info, err := fileid.Stat(local)
	if err != nil {
		return err
	}
	d.at(start)
	if _, err := d.st.StartJob(ctx, jobID, identityJSON(info), backend.Label()); err != nil {
		return err
	}
	out, err := d.outcome(job, p, backend, h, speed, took, settings)
	if err != nil {
		return err
	}
	if out.Status == store.JobComplete {
		expiry := finish.Add(time.Duration(settings.BackupDays) * 24 * time.Hour)
		out.BackupExpiresAt = &expiry
	}
	d.at(finish)
	if err := d.st.FinishJob(ctx, jobID, out); err != nil {
		return err
	}
	if out.Status != store.JobComplete {
		return nil
	}
	return d.afterComplete(ctx, e, job, h, *out.OutputSize, finish, settings.BackupDays)
}

// outcome is how the job ended, with diagnostics like the pipeline's.
func (d *demo) outcome(job store.Job, p plan.Plan, backend encoder.Backend, h pastJob, speed float64,
	took time.Duration, settings store.Settings) (store.JobOutcome, error) {
	srcJSON, srcFrames, ok := d.probe.source(job.LocalPath)
	if !ok {
		return store.JobOutcome{}, fmt.Errorf("no probe for %s", job.LocalPath)
	}
	src, err := media.Parse(srcJSON, srcFrames, p.SourceSize)
	if err != nil {
		return store.JobOutcome{}, err
	}
	args, err := encoder.BuildArgs(backend, encoder.Job{Plan: p, Source: src, Input: job.LocalPath,
		Output: pipeline.PartialPath(job.LocalPath, job.ID), JobID: job.ID})
	if err != nil {
		return store.JobOutcome{}, err
	}
	diag := pipeline.Diagnostics{Step: "encoding", Encoder: backend.Label(), Args: args, Speed: speed, EncodeTime: units.Duration(took)}
	switch h.Result {
	case failed:
		diag.Error = "exited with code 69: Conversion failed!"
		diag.StderrTail = failedStderr
		return store.JobOutcome{Status: store.JobFailed, Diagnostics: mustJSON(diag),
			Summary: fmt.Sprintf("Encoding failed at 61%%. %s returned an error. The original is unchanged.", backend.Label())}, nil
	case tooSmall:
		size := p.SourceSize * 93 / 100
		checks, err := d.validate(src, srcJSON, p, size, settings.MinSavingPercent)
		if err != nil {
			return store.JobOutcome{}, err
		}
		diag.Step, diag.Checks = "validating", checks
		last := checks[len(checks)-1]
		return store.JobOutcome{Status: store.JobSkipped, Diagnostics: mustJSON(diag),
			Summary: last.Detail + " The new file was discarded and the original is unchanged."}, nil
	}
	size := p.EstMin + int64(float64(p.EstMax-p.EstMin)*(0.25+jitter(h.Item+"size")*0.5))
	checks, err := d.validate(src, srcJSON, p, size, settings.MinSavingPercent)
	if err != nil {
		return store.JobOutcome{}, err
	}
	diag.Step, diag.Checks = "replacing", append(checks, pipeline.Check{Name: "decode", Pass: true, Detail: "decoded without errors"})
	backup := pipeline.BackupPath(job.LocalPath, job.ID)
	return store.JobOutcome{
		Status: store.JobComplete, Diagnostics: mustJSON(diag), OutputSize: &size, BackupPath: backup,
		Summary: fmt.Sprintf("%s → %s. %s → %s. Saved %s.", p.SourceLabel, p.TargetLabel,
			units.Bytes(p.SourceSize), units.Bytes(size), units.Bytes(p.SourceSize-size)),
	}, nil
}

// validate runs the pipeline's own checks on the file the job would have
// written.
func (d *demo) validate(src *media.File, srcJSON []byte, p plan.Plan, size int64, minSaving int) ([]pipeline.Check, error) {
	outJSON, outFrames, err := d.fx.outputProbe(srcJSON, p, size, src.Duration)
	if err != nil {
		return nil, err
	}
	out, err := media.Parse(outJSON, outFrames, size)
	if err != nil {
		return nil, err
	}
	out.Container = src.Container
	return pipeline.Validate(src, out, p, minSaving), nil
}

// afterComplete swaps the new file in (by resizing the sparse file), keeps
// or expires the backup, and handles a later restore.
func (d *demo) afterComplete(ctx context.Context, e *entry, job store.Job, h pastJob, size int64, finish time.Time, backupDays int) error {
	local := e.Local(d.dir.media())
	backup := pipeline.BackupPath(local, job.ID)
	expiry := finish.Add(time.Duration(backupDays) * 24 * time.Hour)
	if h.Result == restored {
		d.at(finish.Add(20 * time.Hour))
		if err := d.st.ClearBackup(ctx, job.ID, true); err != nil {
			return err
		}
		return d.st.ExcludeItem(ctx, e.ID, restoredReason)
	}
	if err := d.dir.resize(local, size, finish); err != nil {
		return err
	}
	d.jf.resize(e.Key, size)
	info, err := fileid.Stat(local)
	if err != nil {
		return err
	}
	id := store.FileIdentity{Dev: info.Dev, Inode: info.Inode, Size: info.Size, MtimeNs: info.MtimeNs}
	if err := d.st.SetJobOutputIdentity(ctx, job.ID, mustJSON(id)); err != nil {
		return err
	}
	if err := d.st.RecordOptimised(ctx, id, e.ID, job.ID); err != nil {
		return err
	}
	if expiry.Before(d.now()) {
		d.at(expiry)
		return d.st.ClearBackup(ctx, job.ID, false)
	}
	return d.dir.sparseFile(backup, e.Size, e.Added)
}

// registerOutputs tells the fake prober about every file a past job
// replaced, so probing it again shows the new file.
func (d *demo) registerOutputs(ctx context.Context) error {
	jobs, err := d.st.HistoryJobs(ctx, store.JobComplete, 500, 0)
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if j.RestoredAt != nil || j.OutputSize == nil {
			continue
		}
		srcJSON, _, ok := d.probe.source(j.LocalPath)
		if !ok {
			continue
		}
		var p plan.Plan
		if err := json.Unmarshal([]byte(j.Plan), &p); err != nil {
			return err
		}
		outJSON, outFrames, err := d.fx.outputProbe(srcJSON, p, *j.OutputSize, time.Duration(j.DurationMs)*time.Millisecond)
		if err != nil {
			return err
		}
		d.probe.addOutput(j.LocalPath, *j.OutputSize, outJSON, outFrames)
	}
	return nil
}

// seedQueue queues the Queue table's items. The first was queued by hand.
func (d *demo) seedQueue(ctx context.Context) error {
	defer d.st.SetClock(d.now)
	now := d.now()
	for i, key := range queued {
		trigger := "auto"
		if i == 0 {
			trigger = "manual"
		}
		d.at(now.Add(-time.Duration(len(queued)-i) * 37 * time.Minute))
		if _, _, err := d.queue.Enqueue(ctx, d.cat.byKey[key].ID, trigger); err != nil {
			return fmt.Errorf("queueing %q (the policies must choose it): %w", key, err)
		}
	}
	return nil
}

// Progress of the job -running shows.
const (
	runningFraction = 0.43
	runningSpeed    = 0.71
)

// startRunning shows one waiting job as encoding, as the queue would record
// it. Nothing runs: the demo never starts the queue's workers.
func (d *demo) startRunning(ctx context.Context) error {
	jobs, err := d.st.ActiveJobs(ctx)
	if err != nil {
		return err
	}
	e := d.cat.byKey[running]
	var job *store.Job
	for i := range jobs {
		if jobs[i].ItemID == e.ID && jobs[i].Status == store.JobWaiting {
			job = &jobs[i]
		}
	}
	if job == nil {
		return fmt.Errorf("%q is not waiting in the queue; use -reset", running)
	}
	info, err := fileid.Stat(e.Local(d.dir.media()))
	if err != nil {
		return err
	}
	elapsed := time.Duration(float64(e.Duration) * runningFraction / runningSpeed)
	left := int64((float64(e.Duration) * (1 - runningFraction) / runningSpeed) / float64(time.Second))
	d.at(d.now().Add(-elapsed))
	defer d.st.SetClock(d.now)
	backend, _ := d.reg.Backend(encoder.NameX265)
	if _, err := d.st.StartJob(ctx, job.ID, identityJSON(info), backend.Label()); err != nil {
		return err
	}
	if err := d.st.SetJobStatus(ctx, job.ID, store.JobEncoding); err != nil {
		return err
	}
	return d.st.SetJobProgress(ctx, job.ID, runningFraction, runningSpeed, &left)
}

// stopRunning puts a job left running by an earlier -running start back
// to waiting.
func (d *demo) stopRunning(ctx context.Context) error {
	jobs, err := d.st.ActiveJobs(ctx)
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if j.Status != store.JobWaiting {
			if err := d.st.RequeueJob(ctx, j.ID, ""); err != nil {
				return err
			}
		}
	}
	return nil
}

func identityJSON(i fileid.Info) string {
	return mustJSON(store.FileIdentity{Dev: i.Dev, Inode: i.Inode, Size: i.Size, MtimeNs: i.MtimeNs})
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // plain structs always marshal
	}
	return string(b)
}

// failedStderr is the end of ffmpeg's output for the failed job: the
// source file has a damaged stretch.
const failedStderr = `[matroska,webm @ 0x55f1c3a2e840] Read error at pos. 30271485952 (0x70c5a8000)
[hevc @ 0x55f1c3b61200] Invalid NAL unit size (1839 > 1022).
[hevc @ 0x55f1c3b61200] Error parsing NAL unit #4.
[vist#0:0/hevc @ 0x55f1c3a31c00] Error while decoding stream #0:0: Invalid data found when processing input
[vist#0:0/hevc @ 0x55f1c3a31c00] Decode error rate 0.0213 exceeds maximum 0.0100
Conversion failed!`
