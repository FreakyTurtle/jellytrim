package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/freakyturtle/jellytrim/internal/encoder"
	"github.com/freakyturtle/jellytrim/internal/ffmpeg"
	"github.com/freakyturtle/jellytrim/internal/pipeline"
	"github.com/freakyturtle/jellytrim/internal/plan"
	"github.com/freakyturtle/jellytrim/internal/store"
	"github.com/freakyturtle/jellytrim/internal/timetable"
	"github.com/freakyturtle/jellytrim/internal/units"
)

// dispatch starts waiting jobs while there is capacity, the queue is not
// paused, Dry Run is off and the processing schedule is active. When the
// schedule turns inactive it stops running encodes (see enforceSchedule).
func (q *Service) dispatch(ctx context.Context) {
	defer q.wg.Done()
	for {
		wait := q.enforceSchedule(ctx)
		q.startReady(ctx)
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			q.mu.Lock()
			for _, cancel := range q.running {
				cancel()
			}
			q.mu.Unlock()
			return
		case <-q.wake:
		case <-timer.C:
		}
		timer.Stop()
	}
}

// pollInterval bounds how long the dispatcher sleeps, so settings changes
// and new jobs are noticed even without a wake-up.
const pollInterval = 15 * time.Second

// enforceSchedule stops running jobs when the schedule is inactive, and
// returns how long to sleep: until the next schedule change or pollInterval,
// whichever is sooner.
func (q *Service) enforceSchedule(ctx context.Context) time.Duration {
	_, active, next, changes := q.Schedule(ctx)
	if !active {
		q.stopForSchedule()
	}
	if changes {
		if d := time.Until(next); d > 0 && d < pollInterval {
			return d + 50*time.Millisecond
		}
	}
	return pollInterval
}

// stopForSchedule cancels running jobs so they are requeued. A job that is
// already replacing is left alone: its encode is done, so it uses no
// encoder while it waits for playback to end, and its last step is short
// and uninterruptible.
func (q *Service) stopForSchedule() {
	q.mu.Lock()
	defer q.mu.Unlock()
	for id, cancel := range q.running {
		if q.live[id].Status == store.JobReplacing {
			continue
		}
		if !q.scheduleStopped[id] {
			q.scheduleStopped[id] = true
			q.log.Info("queue: stopping job outside the processing schedule", "job", id)
			cancel()
		}
	}
}

// Schedule reports the processing schedule and its state now: whether it is
// active, and when it next changes (changes is false for "any time" and
// "never").
func (q *Service) Schedule(ctx context.Context) (week timetable.Week, active bool, next time.Time, changes bool) {
	st, err := q.store.Settings(ctx)
	if err == nil {
		week, err = timetable.Parse(st.ProcessingSchedule)
	}
	if err != nil {
		// An unreadable schedule must not start encodes at the wrong time.
		return timetable.Week{}, false, time.Time{}, false
	}
	now := q.now()
	next, changes = week.NextChange(now)
	return week, week.Active(now), next, changes
}

func (q *Service) startReady(ctx context.Context) {
	st, err := q.store.Settings(ctx)
	if err != nil || st.DryRun || q.Paused() {
		q.setHold(nil) // nothing would start anyway, so nothing is held for space
		return
	}
	if _, active, _, _ := q.Schedule(ctx); !active {
		q.setHold(nil)
		return
	}
	for {
		q.mu.Lock()
		busy := len(q.running)
		q.mu.Unlock()
		if busy >= max(st.Concurrency, 1) {
			return
		}
		j, r, ok := q.nextStartable(ctx)
		if !ok {
			return
		}
		ok, err := q.store.StartJob(ctx, j.ID, "", "")
		if err != nil || !ok {
			return
		}
		jobCtx, cancel := context.WithCancel(ctx)
		q.mu.Lock()
		q.running[j.ID] = cancel
		q.reserved[j.ID] = r
		q.live[j.ID] = Live{Status: store.JobAnalysing}
		q.mu.Unlock()
		q.wg.Add(1)
		go func(j store.Job) {
			defer q.wg.Done()
			q.process(jobCtx, j)
			cancel()
			q.mu.Lock()
			delete(q.running, j.ID)
			delete(q.reserved, j.ID)
			delete(q.live, j.ID)
			delete(q.cancelled, j.ID)
			delete(q.scheduleStopped, j.ID)
			q.mu.Unlock()
			q.Wake()
		}(j)
	}
}

// process runs one job from analysis to its final state.
func (q *Service) process(ctx context.Context, j store.Job) {
	finish := func(status, summary string, diag pipeline.Diagnostics) {
		q.finish(j, store.JobOutcome{Status: status, Summary: summary, Diagnostics: mustJSON(diag)})
	}
	unlock, ok := q.lockItem(j.ItemID)
	if !ok {
		finish(store.JobSkipped, "Another action on this item was in progress, so the job did not start.", pipeline.Diagnostics{Step: "analysing"})
		return
	}
	defer unlock()
	p, err := q.playingAtStart(ctx, j.ItemID)
	if err != nil {
		delay := q.playback.StartDelay
		q.deferItem(j.ItemID, delay)
		_ = q.store.RequeueJob(context.WithoutCancel(ctx), j.ID,
			fmt.Sprintf("%s JellyTrim will try again in %s.", err.Error(), units.Duration(delay)))
		return
	}
	if p != nil {
		// Someone is watching it: encoding now would only end in a long
		// wait before the replacement. Let other jobs go first.
		delay := q.playback.StartDelay
		q.deferItem(j.ItemID, delay)
		_ = q.store.RequeueJob(context.WithoutCancel(ctx), j.ID,
			fmt.Sprintf("Not started: %s. JellyTrim will try again in %s; other jobs go first.",
				strings.TrimPrefix(waitingNote(*p), "Waiting: "), units.Duration(delay)))
		return
	}
	// The decision must not rest on watch state from the last sync, which
	// can be hours old. If Jellyfin cannot answer, the stored state is used.
	if err := q.library.RefreshItemUserData(ctx, j.ItemID); err != nil {
		q.log.Warn("queue: could not re-read watch state; using the last sync's", "job", j.ID, "err", err)
	}
	a, err := q.library.Reassess(ctx, j.ItemID)
	if err != nil && ctx.Err() != nil {
		// Stopped while analysing: nothing was written; run it again later.
		q.record(j, pipeline.Job{}, pipeline.Result{Outcome: pipeline.Interrupted}, 0)
		return
	}
	if err != nil {
		finish(store.JobSkipped, "JellyTrim could not re-check the file before starting: "+err.Error(), pipeline.Diagnostics{Step: "analysing", Error: err.Error()})
		return
	}
	if a.Decision.Outcome != plan.Optimise || a.Decision.Plan == nil {
		finish(store.JobSkipped, "Nothing to do after re-checking: "+a.Decision.Summary, pipeline.Diagnostics{Step: "analysing"})
		return
	}
	pl := *a.Decision.Plan
	if a.File != nil {
		a.Candidate.File = a.File
	}
	if item := a.Candidate.Item; item.LocalPath != j.LocalPath || item.JellyfinPath != j.JellyfinPath {
		// The file moved since the job was queued. Record the new paths
		// before any journal entry, so recovery and Restore use them.
		if err := q.store.UpdateJobPaths(ctx, j.ID, item.LocalPath, item.JellyfinPath); err != nil {
			finish(store.JobFailed, "JellyTrim could not record the file's new location, so it did not start.", pipeline.Diagnostics{Step: "analysing", Error: err.Error()})
			return
		}
		j.LocalPath, j.JellyfinPath = item.LocalPath, item.JellyfinPath
	}
	backend, ok := q.registry.Backend(pl.Encoder)
	if !ok {
		finish(store.JobSkipped, "No working encoder is available for this file.", pipeline.Diagnostics{Step: "analysing"})
		return
	}
	overrides := overridesFrom(a.Settings)
	pj := pipeline.Job{
		ID: j.ID, Path: a.Candidate.Item.LocalPath, Plan: pl, Source: a.Candidate.File, Identity: a.Identity,
		Encoder: backend.Label(), Roots: a.Roots, MinSavingPercent: a.Settings.MinSavingPercent,
		SampledDecode: a.Settings.Validation == "sampled",
		Args: func(in, out string) ([]string, error) {
			return encoder.BuildArgs(backend, encoder.Job{Plan: pl, Source: a.Candidate.File, Input: in, Output: out, JobID: j.ID, Overrides: overrides})
		},
	}
	pj.BeforeReplace = func(ctx context.Context) (string, error) { return q.beforeReplace(ctx, j, pj) }
	identity, _ := json.Marshal(store.FileIdentity{Dev: a.Identity.Dev, Inode: a.Identity.Inode, Size: a.Identity.Size, MtimeNs: a.Identity.MtimeNs})
	_ = q.store.SetJobStatusDetail(ctx, j.ID, store.JobAnalysing, string(identity), backend.Label())
	_ = q.store.SetJobPartial(ctx, j.ID, pipeline.PartialPath(pj.Path, j.ID))

	res := q.pipe.Execute(ctx, pj, q.hooks(ctx, j.ID, a.Candidate.File.Duration, backend.Label()))
	q.record(j, pj, res, a.Settings.BackupDays)
}

func (q *Service) hooks(ctx context.Context, jobID int64, total time.Duration, enc string) pipeline.Hooks {
	var lastSave time.Time
	return pipeline.Hooks{
		Status: func(status string) {
			q.setLive(jobID, func(l *Live) { l.Status = status; l.Encoder = enc })
			_ = q.store.SetJobStatus(context.WithoutCancel(ctx), jobID, status)
		},
		Note: func(note string) { q.setNote(jobID, note) },
		Progress: func(p ffmpeg.Progress) {
			var eta time.Duration
			if p.Speed > 0 && total > 0 {
				eta = time.Duration(float64(total-p.OutTime) / p.Speed)
			}
			q.setLive(jobID, func(l *Live) { l.Fraction, l.Speed, l.ETA = p.Fraction, p.Speed, eta })
			if time.Since(lastSave) > 3*time.Second {
				lastSave = time.Now()
				secs := int64(eta.Seconds())
				_ = q.store.SetJobProgress(context.WithoutCancel(ctx), jobID, p.Fraction, p.Speed, &secs)
			}
		},
	}
}

func (q *Service) setLive(jobID int64, fn func(*Live)) {
	q.mu.Lock()
	l := q.live[jobID]
	fn(&l)
	q.live[jobID] = l
	q.mu.Unlock()
}

// record stores a job's outcome and follows up a replacement.
func (q *Service) record(j store.Job, pj pipeline.Job, res pipeline.Result, backupDays int) {
	o := store.JobOutcome{Summary: res.Summary, Diagnostics: mustJSON(res.Diagnostics)}
	switch res.Outcome {
	case pipeline.Complete:
		o.Status = store.JobComplete
		size := res.OutputSize
		o.OutputSize = &size
		o.BackupPath = res.BackupPath
		exp := q.now().Add(time.Duration(backupDays) * 24 * time.Hour)
		o.BackupExpiresAt = &exp
		saved := pj.Source.Size - size
		o.Summary = fmt.Sprintf("%s → %s. %s → %s. Saved %s.", pj.Plan.SourceLabel, pj.Plan.TargetLabel,
			units.Bytes(pj.Source.Size), units.Bytes(size), units.Bytes(saved))
		ctx := context.WithoutCancel(q.baseCtx())
		id := store.FileIdentity{Dev: res.Output.Dev, Inode: res.Output.Inode, Size: res.Output.Size, MtimeNs: res.Output.MtimeNs}
		o.OutputIdentity = mustJSON(id)
		_ = q.store.RecordOptimised(ctx, id, j.ItemID, j.ID)
	case pipeline.NeedsAttention:
		o.Status, o.BackupPath = store.JobAttention, res.BackupPath
	case pipeline.Skipped:
		o.Status = store.JobSkipped
		if res.BackupPath != "" {
			// Another program replaced the file; the old version is kept
			// like any backup and expires with them.
			o.BackupPath = res.BackupPath
			exp := q.now().Add(time.Duration(backupDays) * 24 * time.Hour)
			o.BackupExpiresAt = &exp
		}
	case pipeline.Interrupted:
		q.mu.Lock()
		byUser := q.cancelled[j.ID]
		q.mu.Unlock()
		if !byUser {
			_ = q.store.RequeueJob(context.WithoutCancel(q.baseCtx()), j.ID, q.requeueSummary(j.ID, res))
			return
		}
		o.Status, o.Summary = store.JobCancelled, "Cancelled while running. The original is unchanged."
	default:
		o.Status = store.JobFailed
	}
	q.finish(j, o)
	if res.Outcome == pipeline.Complete {
		q.afterReplace(j)
	}
}

// requeueSummary explains why an interrupted job is waiting again: the
// schedule ended, JellyTrim is shutting down, or the pipeline's own reason
// (someone kept watching the file).
func (q *Service) requeueSummary(jobID int64, res pipeline.Result) string {
	q.mu.Lock()
	bySchedule := q.scheduleStopped[jobID]
	q.mu.Unlock()
	switch {
	case bySchedule:
		return "Stopped because the processing schedule ended. It will start again in the next active hour; the original is unchanged."
	case q.baseCtx().Err() != nil || res.Summary == "":
		return "Interrupted by a restart; it will run again."
	}
	return res.Summary
}

func (q *Service) finish(j store.Job, o store.JobOutcome) {
	q.forgetGiveUps(j.ID)
	ctx := context.WithoutCancel(q.baseCtx())
	if err := q.store.FinishJob(ctx, j.ID, o); err != nil {
		q.log.Error("queue: recording job result", "job", j.ID, "err", err)
	}
	q.log.Info("queue: job finished", "job", j.ID, "item", j.ItemID, "status", o.Status)
}

func (q *Service) baseCtx() context.Context {
	if q.base == nil {
		return context.Background()
	}
	return q.base
}

// afterReplace tells Jellyfin about the new file, re-inspects it, and
// deletes the backup straight away if the retention is zero days.
func (q *Service) afterReplace(j store.Job) {
	ctx := context.WithoutCancel(q.baseCtx())
	_ = q.library.ReprobeItem(ctx, j.ItemID)
	q.wg.Add(1)
	go func() {
		defer q.wg.Done()
		q.notifyJellyfin(q.baseCtx(), j)
		q.library.RunProbeAsync()
	}()
}

func overridesFrom(st store.Settings) encoder.Overrides {
	o := encoder.Overrides{Preset: st.X265Preset}
	if st.QualityOverrides != "" {
		_ = json.Unmarshal([]byte(st.QualityOverrides), &o.Quality)
	}
	return o
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

var errNoUser = errors.New("no Jellyfin user to read the item with")

// lockItem stops two actions (a job, a Restore) working on one item at once.
func (q *Service) lockItem(itemID string) (func(), bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.itemBusy == nil {
		q.itemBusy = map[string]bool{}
	}
	if q.itemBusy[itemID] {
		return nil, false
	}
	q.itemBusy[itemID] = true
	return func() {
		q.mu.Lock()
		delete(q.itemBusy, itemID)
		q.mu.Unlock()
	}, true
}
