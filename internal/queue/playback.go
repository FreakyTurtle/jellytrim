package queue

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/freakyturtle/jellytrim/internal/jellyfin"
	"github.com/freakyturtle/jellytrim/internal/library"
	"github.com/freakyturtle/jellytrim/internal/pipeline"
	"github.com/freakyturtle/jellytrim/internal/units"
)

// PlaybackWait sets how a job whose new file is ready waits for people to
// stop watching the original. Zero fields take the defaults.
type PlaybackWait struct {
	// Poll is how often Jellyfin is asked what is playing (default 30 s).
	Poll time.Duration
	// Limit is how long a job waits for playback to end before it gives up
	// and runs again later (default 6 hours).
	Limit time.Duration
	// Unreachable is how long Jellyfin may fail to answer before the job
	// goes ahead without knowing (default 10 minutes).
	Unreachable time.Duration
	// StartDelay is how long a job is held back when its file is playing at
	// the moment it would start (default 30 minutes).
	StartDelay time.Duration
	// RetryDelay is how long a job is held back after it gave up waiting
	// for playback to end (default 2 hours), so a client left paused does
	// not cause an encode loop.
	RetryDelay time.Duration
}

// Defaults for PlaybackWait.
const (
	DefaultPlaybackPoll        = 30 * time.Second
	DefaultPlaybackLimit       = 6 * time.Hour
	DefaultPlaybackUnreachable = 10 * time.Minute
	DefaultPlaybackStartDelay  = 30 * time.Minute
	DefaultPlaybackRetryDelay  = 2 * time.Hour
)

// restoreCheckTimeout bounds how long Restore waits for Jellyfin's answer.
const restoreCheckTimeout = 15 * time.Second

func (w PlaybackWait) withDefaults() PlaybackWait {
	if w.Poll <= 0 {
		w.Poll = DefaultPlaybackPoll
	}
	if w.Limit <= 0 {
		w.Limit = DefaultPlaybackLimit
	}
	if w.Unreachable <= 0 {
		w.Unreachable = DefaultPlaybackUnreachable
	}
	if w.StartDelay <= 0 {
		w.StartDelay = DefaultPlaybackStartDelay
	}
	if w.RetryDelay <= 0 {
		w.RetryDelay = DefaultPlaybackRetryDelay
	}
	return w
}

// sentence is an error whose text is one or more complete sentences, shown
// to people as it is.
type sentence string

func (s sentence) Error() string { return string(s) }

// Errors from Restore about playback.
var (
	// ErrInUse means someone is playing the item. Restore returns an
	// *InUseError, which names who, and matches ErrInUse with errors.Is.
	ErrInUse error = sentence("Someone is watching this right now. Try again when they have finished.")
	// ErrPlaybackUnknown means Jellyfin could not say whether the item is
	// playing, so JellyTrim did not risk it. Restore can be asked to go
	// ahead anyway.
	ErrPlaybackUnknown error = sentence("JellyTrim could not check with Jellyfin whether this is playing. Try again in a moment.")
	// ErrTooLate means Cancel came after the job started replacing the file.
	ErrTooLate error = sentence("Too late to cancel: JellyTrim is replacing the file now. The original is kept as a backup.")
)

// InUseError says who is playing an item.
type InUseError struct {
	Playing jellyfin.Playing
}

// Error names the viewer and device where Jellyfin reports them.
func (e *InUseError) Error() string {
	return "Someone is watching this right now (" + whoAndWhere(e.Playing) +
		"). Try again when they have finished."
}

// Is matches ErrInUse.
func (e *InUseError) Is(target error) bool { return target == ErrInUse }

// UnreachableWarning is kept in a job's diagnostics when it replaced a file
// without Jellyfin confirming that nobody was playing it.
const UnreachableWarning = "Jellyfin could not be asked whether the file was playing; it was replaced anyway."

// errStillPlaying is the give-up error; its text becomes the job's summary.
func errStillPlaying(limit time.Duration) error {
	return sentence("Someone was still watching this after " + longDuration(limit) + ", so JellyTrim will try again later.")
}

// errCannotConfirm is the give-up error when Jellyfin stopped answering (or
// rejected the key) and JellyTrim could not confirm playback had ended.
func errCannotConfirm(limit time.Duration) error {
	return sentence("JellyTrim could not confirm with Jellyfin that nobody was watching this after " +
		longDuration(limit) + ", so it will try again later.")
}

// maxGiveUps is how many times a job may give up waiting for playback to end
// before it is skipped, so a file someone always seems to be watching does
// not cause an endless loop of encodes.
const maxGiveUps = 3

// errGaveUpForGood is the summary of a job skipped after maxGiveUps.
const errGaveUpForGood = "Someone was watching this each time it was ready, so JellyTrim stopped trying. " +
	"Queue it again to retry. The original is unchanged."

// giveUp holds the item back for the retry delay and counts the give-up. It
// returns err, which sends the job back to the queue, or, at the
// maxGiveUps-th give-up, a stop that ends the job as Skipped.
func (q *Service) giveUp(jobID int64, itemID string, err error) error {
	q.deferItem(itemID, q.playback.RetryDelay)
	q.mu.Lock()
	q.giveUps[jobID]++
	n := q.giveUps[jobID]
	q.mu.Unlock()
	q.log.Info("queue: gave up waiting for playback to end", "job", jobID, "item", itemID, "times", n)
	if n >= maxGiveUps {
		return &pipeline.StopError{Outcome: pipeline.Skipped, Summary: errGaveUpForGood}
	}
	return err
}

// cannotConfirm reports errors that say nothing about whether the item is
// playing and will not clear up by waiting a few minutes: a rejected API key
// or no connection set up. They never count as "Jellyfin is down".
func cannotConfirm(err error) bool {
	return errors.Is(err, jellyfin.ErrUnauthorized) || errors.Is(err, library.ErrNotConfigured)
}

// waitState is what waitUntilFree has learned so far.
type waitState struct {
	started      time.Time
	failingSince time.Time // when Jellyfin started failing to answer, or zero
	seen         bool      // someone was playing the item during this wait
	free         int       // "not playing" answers in a row
}

// waitUntilFree returns once nobody is playing the item, polling Jellyfin.
// seen reports whether anyone was playing it during the wait, so the caller
// re-reads the watch state before replacing. Once someone was seen playing,
// it needs two "not playing" answers in a row, a poll apart, so a player
// between episodes or restarting a stream does not count as finished.
//
// It gives up after the limit (see giveUp). If Jellyfin cannot be asked for
// longer than the unreachable grace period, it returns a warning and lets
// the replacement go ahead: the rename is atomic, a player that has the file
// open keeps reading the old copy, and a Jellyfin that cannot answer is
// usually not streaming either. It never goes ahead that way after it has
// seen the item playing, or when Jellyfin rejects the key or is not set up:
// then it keeps asking until the limit and gives up.
func (q *Service) waitUntilFree(ctx context.Context, jobID int64, itemID string) (warning string, seen bool, err error) {
	st := waitState{started: time.Now()}
	for {
		asked := time.Now()
		p, askErr := q.askPlaying(ctx, itemID)
		if ctx.Err() != nil {
			return "", st.seen, ctx.Err()
		}
		if done, warning, err := q.waitStep(jobID, itemID, &st, asked, p, askErr); done {
			return warning, st.seen, err
		}
		if err := sleepCtx(ctx, q.playback.Poll); err != nil {
			return "", st.seen, err
		}
	}
}

// waitStep handles one answer from Jellyfin. done is true when the wait is
// over: with nil (go ahead), a warning (go ahead unchecked) or a give-up.
func (q *Service) waitStep(jobID int64, itemID string, st *waitState, asked time.Time, p *jellyfin.Playing,
	askErr error) (done bool, warning string, err error) {
	w := q.playback
	switch {
	case askErr != nil:
		st.free = 0
		if st.failingSince.IsZero() {
			st.failingSince = asked
		}
		if !st.seen && !cannotConfirm(askErr) && time.Since(st.failingSince) >= w.Unreachable {
			q.log.Warn("queue: replacing without a playback check; Jellyfin did not answer",
				"job", jobID, "for", time.Since(st.failingSince).Round(time.Second), "err", askErr)
			return true, UnreachableWarning, nil
		}
		if time.Since(st.started) >= w.Limit {
			return true, "", q.giveUp(jobID, itemID, errCannotConfirm(w.Limit))
		}
		q.setNote(jobID, "Waiting: checking with Jellyfin that nobody is watching this.")
	case p == nil:
		st.failingSince = time.Time{}
		st.free++
		if !st.seen || st.free >= 2 {
			return true, "", nil
		}
		q.setNote(jobID, "Waiting: checking that they have finished watching.")
	default:
		st.seen, st.free, st.failingSince = true, 0, time.Time{}
		if time.Since(st.started) >= w.Limit {
			return true, "", q.giveUp(jobID, itemID, errStillPlaying(w.Limit))
		}
		q.setNote(jobID, waitingNote(*p))
	}
	return false, "", nil
}

// askPlaying asks Jellyfin who is playing the item. Tests replace it with a
// scripted sequence of answers.
func (q *Service) askPlaying(ctx context.Context, itemID string) (*jellyfin.Playing, error) {
	if q.playingFn != nil {
		return q.playingFn(ctx, itemID)
	}
	return q.playingItem(ctx, itemID)
}

// playingItem returns a session playing the item (paused counts), or nil.
func (q *Service) playingItem(ctx context.Context, itemID string) (*jellyfin.Playing, error) {
	c, err := q.library.Client(ctx)
	if err != nil {
		return nil, err
	}
	sessions, err := c.NowPlaying(ctx)
	if err != nil {
		return nil, err
	}
	for _, s := range sessions {
		if s.Plays(itemID) {
			return &s, nil
		}
	}
	return nil, nil
}

// checkNotPlaying refuses with ErrInUse or ErrPlaybackUnknown unless
// Jellyfin confirms nobody is playing the item. With anyway set, a Jellyfin
// that cannot answer does not stop it, but one that reports playback does.
func (q *Service) checkNotPlaying(ctx context.Context, itemID string, anyway bool) error {
	ctx, cancel := context.WithTimeout(ctx, restoreCheckTimeout)
	defer cancel()
	p, err := q.playingItem(ctx, itemID)
	if err != nil && anyway {
		q.log.Warn("queue: restoring without a playback check, as asked; Jellyfin did not answer", "item", itemID, "err", err)
		return nil
	}
	if err != nil {
		q.log.Warn("queue: could not ask Jellyfin what is playing", "item", itemID, "err", err)
		return ErrPlaybackUnknown
	}
	if p != nil {
		return &InUseError{Playing: *p}
	}
	return nil
}

func (q *Service) setNote(jobID int64, note string) {
	q.setLive(jobID, func(l *Live) { l.Note = note })
}

// waitingNote is the queue's note while a job waits: "Waiting: alex is
// watching this on Living Room TV".
func waitingNote(p jellyfin.Playing) string {
	who := p.UserName
	if who == "" {
		who = "someone"
	}
	verb := " is watching this"
	if p.Paused {
		verb = " has this paused"
	}
	where := ""
	if p.DeviceName != "" {
		where = " on " + p.DeviceName
	}
	return "Waiting: " + who + verb + where
}

// whoAndWhere is "alex on Living Room TV", leaving out what is unknown.
func whoAndWhere(p jellyfin.Playing) string {
	who := p.UserName
	if who == "" {
		who = "someone"
	}
	if p.DeviceName == "" {
		return who
	}
	return who + " on " + p.DeviceName
}

// longDuration is "6 hours" for whole hours, otherwise units.Duration.
func longDuration(d time.Duration) string {
	switch {
	case d == time.Hour:
		return "an hour"
	case d > time.Hour && d%time.Hour == 0:
		return fmt.Sprintf("%d hours", d/time.Hour)
	}
	return units.Duration(d)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// deferItem holds an item's jobs back until the delay has passed. The hold
// is in memory only: after a restart the start check below catches a file
// that is still playing.
func (q *Service) deferItem(itemID string, d time.Duration) {
	if d <= 0 {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.deferred == nil {
		q.deferred = map[string]time.Time{}
	}
	q.deferred[itemID] = time.Now().Add(d)
}

// isDeferred reports whether an item's jobs are held back now.
func (q *Service) isDeferred(itemID string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	until, ok := q.deferred[itemID]
	if ok && time.Now().After(until) {
		delete(q.deferred, itemID)
		return false
	}
	return ok
}

// playingAtStart reports who is playing the item when its job would start,
// so the encode is not spent on a file that could not be replaced yet. If
// Jellyfin cannot be reached, the job starts: the wait before replacing
// still applies. If Jellyfin rejects the key or is not set up, it returns
// errKeyRejectedAtStart: the wait could never confirm the file is free, so the
// encode would be wasted.
func (q *Service) playingAtStart(ctx context.Context, itemID string) (*jellyfin.Playing, error) {
	ctx, cancel := context.WithTimeout(ctx, restoreCheckTimeout)
	defer cancel()
	p, err := q.askPlaying(ctx, itemID)
	if err != nil {
		if cannotConfirm(err) {
			return nil, errKeyRejectedAtStart
		}
		return nil, nil
	}
	return p, nil
}

// errKeyRejectedAtStart holds a job back at its start when Jellyfin rejects
// JellyTrim's key or is not set up.
var errKeyRejectedAtStart error = sentence("Not started: Jellyfin rejected JellyTrim's API key or is not set up, " +
	"so JellyTrim cannot check whether this file is being played. Check the connection in Settings.")
