// Package queue runs optimisation jobs: it queues items the policies chose,
// runs them through the safe pipeline one (or a few) at a time, records the
// outcome, tells Jellyfin about replaced files, and recovers after a crash.
package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/freakyturtle/jellytrim/internal/encoder"
	"github.com/freakyturtle/jellytrim/internal/ffmpeg"
	"github.com/freakyturtle/jellytrim/internal/library"
	"github.com/freakyturtle/jellytrim/internal/pipeline"
	"github.com/freakyturtle/jellytrim/internal/plan"
	"github.com/freakyturtle/jellytrim/internal/store"
)

// Errors callers branch on.
var (
	ErrDryRun       = errors.New("Dry Run is on, so JellyTrim will not change files. Turn it off in Settings first")
	ErrNotOptimise  = errors.New("JellyTrim has no plan to optimise this item")
	ErrNotCancelled = errors.New("only waiting or running jobs can be cancelled")
	ErrNotRetryable = errors.New("only failed, skipped or cancelled jobs can be retried")
)

// Live is the in-memory progress of a running job.
type Live struct {
	Status   string
	Fraction float64
	Speed    float64
	ETA      time.Duration
	Encoder  string
}

// Service is the queue. It is safe for concurrent use.
type Service struct {
	store    *store.Store
	library  *library.Service
	registry *encoder.Registry
	pipe     *pipeline.Pipeline
	log      *slog.Logger
	now      func() time.Time

	mu        sync.Mutex
	paused    bool
	running   map[int64]context.CancelFunc
	cancelled map[int64]bool // cancelled by the user rather than shut down
	live      map[int64]Live
	itemBusy  map[string]bool // items with a job or Restore in progress
	// scheduleStopped marks running jobs stopped because the schedule ended.
	scheduleStopped map[int64]bool
	wake            chan struct{}
	// space reads free space; hold is the job held back for space, logged
	// at holdLogged; reserved is the space each running job may still use.
	space      SpaceFS
	hold       *SpaceHold
	holdLogged time.Time
	reserved   map[int64]reservation
	wg         sync.WaitGroup
	base       context.Context
}

// Options configure the queue.
type Options struct {
	Store    *store.Store
	Library  *library.Service
	Registry *encoder.Registry
	Runner   *ffmpeg.Runner
	Log      *slog.Logger
	Now      func() time.Time
	// Space reads free space and filesystem IDs; nil uses the real
	// filesystem.
	Space SpaceFS
}

// New builds the queue.
func New(o Options) *Service {
	q := &Service{
		store: o.Store, library: o.Library, registry: o.Registry, log: o.Log, now: o.Now,
		running: map[int64]context.CancelFunc{}, cancelled: map[int64]bool{}, live: map[int64]Live{},
		scheduleStopped: map[int64]bool{}, reserved: map[int64]reservation{},
		wake: make(chan struct{}, 1), space: o.Space,
	}
	if q.space == nil {
		q.space = osSpace{}
	}
	if q.log == nil {
		q.log = slog.Default()
	}
	if q.now == nil {
		q.now = time.Now
	}
	q.pipe = &pipeline.Pipeline{FS: pipeline.OSFS{}, Runner: o.Runner, Journal: o.Store, Log: q.log}
	return q
}

// Pipeline exposes the pipeline for restore and backup operations.
func (q *Service) Pipeline() *pipeline.Pipeline { return q.pipe }

// Start recovers interrupted jobs, then dispatches jobs until ctx ends.
// Stop waits for running jobs to wind down.
func (q *Service) Start(ctx context.Context) {
	q.base = ctx
	if v, err := q.store.Setting(ctx, store.KeyQueuePaused); err == nil && v == "true" {
		q.mu.Lock()
		q.paused = true
		q.mu.Unlock()
	}
	q.recover(ctx)
	q.wg.Add(1)
	go q.dispatch(ctx)
}

// Stop waits for the dispatcher and running jobs after ctx is cancelled.
func (q *Service) Stop() { q.wg.Wait() }

// Wake asks the dispatcher to look for work now.
func (q *Service) Wake() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// Paused reports whether the queue is paused.
func (q *Service) Paused() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.paused
}

// SetPaused pauses or resumes starting new jobs. Running jobs finish.
func (q *Service) SetPaused(p bool) {
	q.mu.Lock()
	q.paused = p
	q.mu.Unlock()
	if err := q.store.SetSetting(context.WithoutCancel(q.baseCtx()), store.KeyQueuePaused, store.FormatBool(p)); err != nil {
		q.log.Warn("queue: saving pause state", "err", err)
	}
	q.Wake()
}

// Live returns the in-memory progress of running jobs.
func (q *Service) Live() map[int64]Live {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make(map[int64]Live, len(q.live))
	for k, v := range q.live {
		out[k] = v
	}
	return out
}

// Enqueue queues one item that the policies chose to optimise.
func (q *Service) Enqueue(ctx context.Context, itemID, trigger string) (int64, bool, error) {
	st, err := q.store.Settings(ctx)
	if err != nil {
		return 0, false, err
	}
	if st.DryRun {
		return 0, false, ErrDryRun
	}
	id, created, err := q.create(ctx, itemID, trigger)
	if err == nil && created {
		q.Wake()
	}
	return id, created, err
}

// create queues one item. The candidate query only returns items whose
// latest evaluation is optimise.
func (q *Service) create(ctx context.Context, itemID, trigger string) (int64, bool, error) {
	c, err := q.store.QueueCandidateFor(ctx, itemID)
	if errors.Is(err, store.ErrNotFound) {
		return 0, false, ErrNotOptimise
	}
	if err != nil {
		return 0, false, err
	}
	j, err := jobFor(c, trigger)
	if err != nil {
		return 0, false, err
	}
	return q.store.CreateJob(ctx, j)
}

// jobFor builds the job row for an item the policies chose.
func jobFor(c store.QueueCandidate, trigger string) (store.Job, error) {
	var p plan.Plan
	if err := json.Unmarshal([]byte(c.Plan), &p); err != nil {
		return store.Job{}, fmt.Errorf("reading the plan for %s: %w", c.ItemID, err)
	}
	name := c.ItemName
	if c.SeriesName != "" && !strings.HasPrefix(c.ItemName, c.SeriesName) {
		name = c.SeriesName + " · " + c.ItemName
	}
	size := p.SourceSize
	return store.Job{
		ItemID: c.ItemID, ItemName: name, LibraryName: c.LibraryName, PolicyID: c.PolicyID, PolicyName: c.PolicyName,
		Trigger: trigger, Plan: c.Plan, LocalPath: c.LocalPath, JellyfinPath: c.JellyfinPath,
		SourceSummary: p.SourceLabel, TargetSummary: p.TargetLabel, SourceSize: &size, EstMin: c.EstMin, EstMax: c.EstMax,
		DurationMs: c.DurationMs,
	}, nil
}

// EnqueueMatching queues every item the policies chose, except items that
// had a job recently (so failures are not retried in a loop). It does
// nothing in Dry Run or when automatic processing is off. The jobs are
// built from one query and inserted in batches, so queueing a large library
// takes seconds rather than minutes.
func (q *Service) EnqueueMatching(ctx context.Context) (int, error) {
	st, err := q.store.Settings(ctx)
	if err != nil || st.DryRun || !st.AutoProcess {
		return 0, err
	}
	candidates, err := q.store.QueueCandidates(ctx)
	if err != nil {
		return 0, err
	}
	recent, err := q.store.ItemsWithRecentJobs(ctx, q.now().Add(-7*24*time.Hour))
	if err != nil {
		return 0, err
	}
	jobs := make([]store.Job, 0, len(candidates))
	for _, c := range candidates {
		if recent[c.ItemID] {
			continue
		}
		j, err := jobFor(c, "auto")
		if err != nil {
			q.log.Warn("queue: could not queue item", "item", c.ItemID, "err", err)
			continue
		}
		jobs = append(jobs, j)
	}
	n, err := q.store.CreateJobs(ctx, jobs)
	if n > 0 {
		q.log.Info("queue: queued items", "count", n)
		q.Wake()
	}
	return n, err
}

// Cancel cancels a waiting job, or stops a running one before it replaces
// anything. The original is never affected.
func (q *Service) Cancel(ctx context.Context, jobID int64) error {
	ok, err := q.store.CancelJob(ctx, jobID)
	if err != nil || ok {
		return err
	}
	q.mu.Lock()
	cancel, running := q.running[jobID]
	if running {
		q.cancelled[jobID] = true
	}
	q.mu.Unlock()
	if !running {
		return ErrNotCancelled
	}
	cancel()
	return nil
}

// Retry queues a finished job's item again, re-evaluating it first.
func (q *Service) Retry(ctx context.Context, jobID int64) (int64, error) {
	j, err := q.store.Job(ctx, jobID)
	if err != nil {
		return 0, err
	}
	switch j.Status {
	case store.JobFailed, store.JobSkipped, store.JobCancelled:
	default:
		return 0, ErrNotRetryable
	}
	if err := q.library.ReprobeItem(ctx, j.ItemID); err != nil {
		return 0, err
	}
	if _, err := q.library.Reassess(ctx, j.ItemID); err != nil {
		return 0, err
	}
	if _, err := q.library.Evaluate(ctx); err != nil && !errors.Is(err, library.ErrBusy) {
		return 0, err
	}
	id, _, err := q.Enqueue(ctx, j.ItemID, "manual")
	return id, err
}
