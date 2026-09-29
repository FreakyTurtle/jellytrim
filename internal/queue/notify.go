package queue

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/freakyturtle/jellytrim/internal/fileid"
	"github.com/freakyturtle/jellytrim/internal/jellyfin"
	"github.com/freakyturtle/jellytrim/internal/pipeline"
	"github.com/freakyturtle/jellytrim/internal/store"
)

// Jellyfin re-reads a changed file after its library monitor delay (60 s by
// default). JellyTrim waits a little longer, then asks for a refresh.
var (
	notifyPoll     = 10 * time.Second
	notifyDeadline = 90 * time.Second
)

// notifyJellyfin tells Jellyfin a file changed and checks that it noticed.
// Jellyfin's /Library/Media/Updated returns 204 even for unknown paths, so
// the only proof is the item's reported size changing.
func (q *Service) notifyJellyfin(ctx context.Context, j store.Job) {
	c, err := q.library.Client(ctx)
	if err != nil {
		q.log.Warn("queue: cannot notify Jellyfin", "job", j.ID, "err", err)
		return
	}
	if err := c.NotifyMediaUpdated(ctx, j.JellyfinPath); err != nil {
		q.log.Warn("queue: notifying Jellyfin", "job", j.ID, "err", err)
	}
	userID, err := q.anyUser(ctx)
	if err != nil {
		return
	}
	job, err := q.store.Job(ctx, j.ID)
	if err != nil || job.OutputSize == nil {
		return
	}
	deadline := time.Now().Add(notifyDeadline)
	refreshed := false
	for {
		if q.jellyfinSeesSize(ctx, c, userID, j.ItemID, *job.OutputSize) {
			q.log.Info("queue: Jellyfin picked up the new file", "job", j.ID)
			return
		}
		if time.Now().After(deadline) {
			if refreshed {
				q.log.Warn("queue: Jellyfin has not picked up the new file yet; the next sync will show it", "job", j.ID)
				return
			}
			refreshed = true
			deadline = time.Now().Add(notifyDeadline)
			if err := c.RefreshItem(ctx, j.ItemID); err != nil {
				q.log.Warn("queue: asking Jellyfin to refresh the item", "job", j.ID, "err", err)
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(notifyPoll):
		}
	}
}

func (q *Service) jellyfinSeesSize(ctx context.Context, c *jellyfin.Client, userID, itemID string, size int64) bool {
	it, err := c.Item(ctx, userID, itemID)
	if err != nil || len(it.MediaSources) == 0 {
		return false
	}
	return it.MediaSources[0].Size == size
}

func (q *Service) anyUser(ctx context.Context) (string, error) {
	users, err := q.store.Users(ctx)
	if err != nil {
		return "", err
	}
	for _, u := range users {
		if u.Selected && !u.Disabled {
			return u.ID, nil
		}
	}
	for _, u := range users {
		if !u.Disabled {
			return u.ID, nil
		}
	}
	return "", errNoUser
}

// recover puts interrupted jobs into a safe, recorded state at start-up.
func (q *Service) recover(ctx context.Context) {
	jobs, err := q.store.InterruptedJobs(ctx)
	if err != nil {
		q.log.Error("queue: listing interrupted jobs", "err", err)
		return
	}
	for _, j := range jobs {
		rec, err := q.pipe.Recover(ctx, j.ID, j.LocalPath)
		if err != nil {
			q.log.Error("queue: recovery needs attention", "job", j.ID, "path", j.LocalPath, "err", err)
			_ = q.store.FinishJob(ctx, j.ID, store.JobOutcome{Status: store.JobFailed,
				Summary: "JellyTrim stopped during this job and could not tidy up automatically: " + err.Error() + " Check the folder for hidden .jellytrim files."})
			continue
		}
		q.log.Info("queue: recovered interrupted job", "job", j.ID, "state", rec.State, "actions", rec.Actions)
		switch rec.State {
		case pipeline.RecoveredReplaced:
			st, _ := q.store.Settings(ctx)
			exp := q.now().Add(time.Duration(st.BackupDays) * 24 * time.Hour)
			var size *int64
			outID := ""
			if info, err := q.pipe.FS.Stat(j.LocalPath); err == nil {
				s := info.Size
				size = &s
				id := store.FileIdentity{Dev: info.Dev, Inode: info.Inode, Size: info.Size, MtimeNs: info.MtimeNs}
				outID = mustJSON(id)
				_ = q.store.RecordOptimised(ctx, id, j.ItemID, j.ID)
			}
			_ = q.store.FinishJob(ctx, j.ID, store.JobOutcome{Status: store.JobComplete, OutputSize: size, BackupPath: rec.BackupPath, OutputIdentity: outID,
				BackupExpiresAt: &exp, Summary: "Completed. JellyTrim restarted just after replacing the file; the original is kept as a backup."})
			q.afterReplace(j)
		case pipeline.RecoveredNeedsAttention:
			_ = q.store.FinishJob(ctx, j.ID, store.JobOutcome{Status: store.JobAttention, BackupPath: rec.BackupPath,
				Summary: "The original is safe at " + rec.BackupPath + ", but another file now occupies its usual place, so JellyTrim did not move it back. Decide which to keep and rename the files yourself."})
		case pipeline.RecoveredRestored:
			_ = q.store.FinishJob(ctx, j.ID, store.JobOutcome{Status: store.JobFailed,
				Summary: "Interrupted during replacement. The original was put back and is unchanged."})
		default:
			_ = q.store.RequeueJob(ctx, j.ID, "Interrupted by a restart; it will run again.")
		}
	}
}

// ExpireBackups deletes backups past their retention. Called periodically.
func (q *Service) ExpireBackups(ctx context.Context) {
	jobs, err := q.store.JobsWithBackups(ctx)
	if err != nil {
		return
	}
	for _, j := range jobs {
		if j.BackupExpiresAt == nil || j.BackupExpiresAt.After(q.now()) {
			continue
		}
		if err := q.pipe.DeleteBackup(ctx, j.ID, j.BackupPath, j.LocalPath); err != nil {
			q.log.Warn("queue: deleting expired backup", "job", j.ID, "err", err)
			continue
		}
		_ = q.store.ClearBackup(ctx, j.ID, false)
		q.log.Info("queue: deleted expired backup", "job", j.ID)
	}
}

// Errors from Restore.
var (
	ErrNoBackup = errors.New("this job has no backup to restore")
	ErrItemBusy = errors.New("another job is working on this item; try again when it has finished")
)

// Restore puts a completed job's original back and tells Jellyfin. It
// refuses with an error matching ErrInUse while someone is playing the item,
// and with ErrPlaybackUnknown when Jellyfin cannot say, unless anyway is
// set: the user chose to restore without the check, for example because
// Jellyfin is down and the backup is about to expire. Playback that Jellyfin
// does report always refuses.
func (q *Service) Restore(ctx context.Context, jobID int64, anyway bool) error {
	j, err := q.store.Job(ctx, jobID)
	if err != nil {
		return err
	}
	if j.Status != store.JobComplete || j.BackupPath == "" {
		return ErrNoBackup
	}
	if active, busy, err := q.store.ActiveJobForItem(ctx, j.ItemID); err != nil {
		return err
	} else if busy && active.ID != j.ID {
		return ErrItemBusy
	}
	unlock, ok := q.lockItem(j.ItemID)
	if !ok {
		return ErrItemBusy
	}
	defer unlock()
	// Renaming over a file someone is watching is safe for an open handle
	// on Linux, but a player that re-opens the path (seeking, changing
	// audio) would jump to a different file. Ask Jellyfin first.
	if err := q.checkNotPlaying(ctx, j.ItemID, anyway); err != nil {
		return err
	}
	var out store.FileIdentity
	_ = json.Unmarshal([]byte(j.OutputIdentity), &out)
	expect := fileid.Info{Dev: out.Dev, Inode: out.Inode, Size: out.Size, MtimeNs: out.MtimeNs}
	if err := q.pipe.Restore(ctx, j.ID, j.BackupPath, j.LocalPath, expect); err != nil {
		return err
	}
	if err := q.store.ClearBackup(ctx, j.ID, true); err != nil {
		return err
	}
	if err := q.store.ExcludeItem(ctx, j.ItemID, "You restored the original, so JellyTrim leaves this item alone until you allow it again."); err != nil {
		return err
	}
	_ = q.library.ReprobeItem(ctx, j.ItemID)
	q.wg.Add(1)
	go func() {
		defer q.wg.Done()
		if c, err := q.library.Client(q.baseCtx()); err == nil {
			_ = c.NotifyMediaUpdated(q.baseCtx(), j.JellyfinPath)
		}
		q.library.RunProbeAsync()
	}()
	return nil
}
