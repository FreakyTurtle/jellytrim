package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/freakyturtle/jellytrim/internal/encoder"
	"github.com/freakyturtle/jellytrim/internal/ffmpeg"
	"github.com/freakyturtle/jellytrim/internal/pipeline"
	"github.com/freakyturtle/jellytrim/internal/plan"
	"github.com/freakyturtle/jellytrim/internal/store"
	"github.com/freakyturtle/jellytrim/internal/units"
)

// dispatch starts waiting jobs while there is capacity, the queue is not
// paused, Dry Run is off and the time is inside the processing window.
func (q *Service) dispatch(ctx context.Context) {
	defer q.wg.Done()
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		q.startReady(ctx)
		select {
		case <-ctx.Done():
			q.mu.Lock()
			for _, cancel := range q.running {
				cancel()
			}
			q.mu.Unlock()
			return
		case <-q.wake:
		case <-tick.C:
		}
	}
}

func (q *Service) startReady(ctx context.Context) {
	st, err := q.store.Settings(ctx)
	if err != nil || st.DryRun || q.Paused() || !InWindow(q.now(), st.WindowStart, st.WindowEnd) {
		return
	}
	for {
		q.mu.Lock()
		busy := len(q.running)
		q.mu.Unlock()
		if busy >= max(st.Concurrency, 1) {
			return
		}
		j, err := q.store.NextWaitingJob(ctx)
		if err != nil {
			return
		}
		ok, err := q.store.StartJob(ctx, j.ID, "", "")
		if err != nil || !ok {
			return
		}
		jobCtx, cancel := context.WithCancel(ctx)
		q.mu.Lock()
		q.running[j.ID] = cancel
		q.live[j.ID] = Live{Status: store.JobAnalysing}
		q.mu.Unlock()
		q.wg.Add(1)
		go func(j store.Job) {
			defer q.wg.Done()
			q.process(jobCtx, j)
			cancel()
			q.mu.Lock()
			delete(q.running, j.ID)
			delete(q.live, j.ID)
			delete(q.cancelled, j.ID)
			q.mu.Unlock()
			q.Wake()
		}(j)
	}
}

// InWindow reports whether now is inside the daily window [start, end)
// given as "HH:MM" local times. An empty window means always. A window that
// wraps midnight (22:00 to 06:00) works.
func InWindow(now time.Time, start, end string) bool {
	s, okS := parseClock(start)
	e, okE := parseClock(end)
	if !okS || !okE || s == e {
		return true
	}
	m := now.Hour()*60 + now.Minute()
	if s < e {
		return m >= s && m < e
	}
	return m >= s || m < e
}

func parseClock(v string) (int, bool) {
	h, m, ok := strings.Cut(strings.TrimSpace(v), ":")
	if !ok {
		return 0, false
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, false
	}
	return hh*60 + mm, true
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
	a, err := q.library.Reassess(ctx, j.ItemID)
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
			_ = q.store.RequeueJob(context.WithoutCancel(q.baseCtx()), j.ID, "Interrupted by a restart; it will run again.")
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

func (q *Service) finish(j store.Job, o store.JobOutcome) {
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
