package queue

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/freakyturtle/jellytrim/internal/pipeline"
	"github.com/freakyturtle/jellytrim/internal/plan"
	"github.com/freakyturtle/jellytrim/internal/store"
)

// Summaries for jobs that did not replace their file at the last moment.
const (
	summaryDryRun = "Not replaced: Dry Run was turned on, so the new file was discarded and nothing was changed. " +
		"The job runs again when Dry Run is off."
	summaryWatchedNoLonger = "Not replaced: someone watched this while it was being converted, and the policies no longer choose it. " +
		"The new file was discarded and the original is unchanged."
	summaryNoLonger = "Not replaced: the policies no longer choose this item (%s). " +
		"The new file was discarded and the original is unchanged."
	summaryChanged   = "The original changed while JellyTrim was working on it, so the new file was discarded. The original is unchanged."
	summaryNoRecheck = "Not replaced: JellyTrim could not re-check the file before replacing it. " +
		"The new file was discarded and the original is unchanged."
)

// errNoWatchState is the give-up error when the watch state could not be
// re-read after someone was seen playing the item.
var errNoWatchState error = sentence("Someone watched this while it was being converted, and JellyTrim could not " +
	"read the new watch state from Jellyfin, so it will try again later.")

// beforeReplace is the pipeline's last step before it touches the original:
// wait until nobody is playing the file, then confirm the job should still
// go ahead with what is true now. Dry Run must still be off, the item's
// watch state is re-read, the policies must still choose the same plan, and
// nobody may have started playing it meanwhile. Only then is the job committed
// (Cancel refuses from then on). Any doubt returns an error, so the new file
// is discarded and the original is left alone.
func (q *Service) beforeReplace(ctx context.Context, j store.Job, pj pipeline.Job) (string, error) {
	warning, seen, err := q.waitUntilFree(ctx, j.ID, j.ItemID)
	if err != nil {
		return "", err
	}
	if err := q.checkDryRun(ctx); err != nil {
		return "", err
	}
	// Someone may have watched or favourited the item during the encode,
	// not only during the wait, so the watch state is always re-read.
	q.setNote(j.ID, "Checking the watch state again before replacing the file.")
	if err := q.library.RefreshItemUserData(ctx, j.ItemID); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		q.log.Warn("queue: could not re-read watch state before replacing", "job", j.ID, "seen", seen, "err", err)
		if seen {
			return "", q.giveUp(j.ID, j.ItemID, errNoWatchState)
		}
		// Nobody was seen watching, so the state read at the job's start
		// stands, as it does when the start check cannot reach Jellyfin.
	}
	if err := q.stillChosen(ctx, j, pj, seen); err != nil {
		return "", err
	}
	// Dry Run could have been turned on while the file was re-checked.
	if err := q.checkDryRun(ctx); err != nil {
		return "", err
	}
	// The re-checks take a few seconds; ask once more so a viewer who has
	// just started is not missed. An error here is ignored: Jellyfin
	// answered moments ago.
	if p, err := q.askPlaying(ctx, j.ItemID); err == nil && p != nil {
		q.deferItem(j.ItemID, q.playback.StartDelay)
		return "", errStartedWatching
	}
	return warning, q.commit(ctx, j.ID)
}

// errStartedWatching sends the job back to the queue when someone started
// watching during the last checks.
var errStartedWatching error = sentence("Someone started watching this just before it was replaced, " +
	"so JellyTrim will try again later.")

// checkDryRun stops the job when Dry Run is on, or when the settings cannot
// be read. The job goes back to the queue, which starts nothing in Dry Run.
func (q *Service) checkDryRun(ctx context.Context) error {
	st, err := q.store.Settings(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		q.log.Warn("queue: reading settings before replacing", "err", err)
		return sentence("JellyTrim could not read its settings before replacing the file, so it will try again later.")
	}
	if st.DryRun {
		return &pipeline.StopError{Outcome: pipeline.Interrupted, Summary: summaryDryRun}
	}
	return nil
}

// stillChosen re-runs the item's decision on the original as it is now,
// with the current policies, exclusions and stored watch state. The job
// goes ahead only if the original is the file it started from and the
// policies still choose the same plan.
func (q *Service) stillChosen(ctx context.Context, j store.Job, pj pipeline.Job, seen bool) error {
	a, err := q.library.Reassess(ctx, j.ItemID)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		q.log.Warn("queue: re-checking before replacing", "job", j.ID, "err", err)
		return &pipeline.StopError{Outcome: pipeline.Skipped, Summary: summaryNoRecheck}
	}
	d := a.Decision
	if d.Outcome == plan.Optimise && d.Plan != nil {
		if !a.Identity.Same(pj.Identity) {
			return &pipeline.StopError{Outcome: pipeline.Skipped, Summary: summaryChanged}
		}
		if samePlan(*d.Plan, pj.Plan) {
			return nil
		}
	}
	summary := summaryWatchedNoLonger
	if !seen {
		summary = fmt.Sprintf(summaryNoLonger, strings.TrimSuffix(d.Summary, "."))
	}
	q.log.Info("queue: not replacing; the item is no longer chosen", "job", j.ID, "outcome", d.Outcome, "why", d.Summary)
	return &pipeline.StopError{Outcome: pipeline.Skipped, Summary: summary}
}

// commit marks the job as replacing the file, unless it was cancelled or is
// stopping. It holds the queue's lock, so Cancel sees either a job it can
// still stop or a committed one, never a job between the two.
func (q *Service) commit(ctx context.Context, jobID int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if q.cancelled[jobID] {
		return context.Canceled
	}
	l := q.live[jobID]
	l.Committed, l.Note = true, ""
	q.live[jobID] = l
	return nil
}

// samePlan reports whether two plans produce the same file. Estimates and
// labels are left out: they describe the plan rather than change it.
func samePlan(a, b plan.Plan) bool {
	for _, p := range []*plan.Plan{&a, &b} {
		p.SourceSize, p.EstMin, p.EstMax, p.SourceLabel, p.TargetLabel = 0, 0, 0, "", ""
	}
	return reflect.DeepEqual(a, b)
}
