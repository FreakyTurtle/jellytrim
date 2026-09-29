package pipeline

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/freakyturtle/jellytrim/internal/fileid"
)

// busyRetries is how often a rename that fails with "busy" (common on SMB
// when another process has the file open) is retried, and how often undoing
// a backup is retried before JellyTrim asks for attention.
const busyRetries = 4

// errChanged means the original is no longer the file the job started on.
var errChanged = errors.New("the original changed")

// replace swaps the validated partial file in for the original, keeping the
// original under a hidden backup name. Each step is journalled first.
//
// Order of operations, with what is on disk if JellyTrim stops after each:
//  0. BeforeReplace: wait until nobody is playing the file, and confirm the
//     job should still go ahead                (original, partial)
//  1. re-check the original is unchanged, and the partial is the file that
//     passed validation                        (original, partial)
//  2. set the partial's mode and owner, fsync  (original, partial)
//  3. hard-link the original to the backup     (original = backup, partial)
//     or, if links are unsupported, rename it  (backup, partial; recovery restores)
//  4. re-check the backup is the original, and the path still holds it
//  5. rename the partial over the original     (new file, backup)
//  6. fsync the directory
func (p *Pipeline) replace(ctx context.Context, j Job, partial string, checked validated, h Hooks, diag Diagnostics) Result {
	diag.Step = "replacing"
	if res, ok := p.beforeReplace(ctx, j, partial, h, &diag); !ok {
		return res
	}
	// From here on a shutdown must not interrupt half-way; each step is short.
	ctx = context.WithoutCancel(ctx)
	fail := func(outcome Outcome, summary string) Result {
		p.removePartial(ctx, j.ID, partial, &diag)
		return Result{Outcome: outcome, Summary: summary, Diagnostics: diag}
	}

	partialInfo, outcome, summary := p.readyPartial(j, partial, checked.info, &diag)
	if outcome != "" {
		return fail(outcome, summary)
	}

	backup := BackupPath(j.Path, j.ID)
	if p.FS.Exists(backup) {
		return fail(Failed, "A backup file with the same name already exists, so JellyTrim stopped. The original is unchanged.")
	}
	renamedBackup, err := p.backup(ctx, j, backup, &diag)
	if err != nil {
		diag.Error = err.Error()
		if errors.Is(err, errChanged) {
			return fail(Skipped, "The original changed while JellyTrim was replacing it, so the new file was discarded. The original is unchanged.")
		}
		return fail(Failed, "JellyTrim could not keep a backup of the original, so it did not replace it. The original is unchanged.")
	}

	entry, err := p.Journal.JournalRecord(ctx, j.ID, StepReplaced, j.Path, partial)
	if err == nil {
		err = p.renameChecked(j, partial, renamedBackup)
	}
	if err != nil && p.renameHappened(j.Path, partialInfo) {
		// The rename reached the disk but reported an error (a lost reply
		// on a network filesystem). The new file is in place.
		diag.Warnings = append(diag.Warnings, "The final rename reported an error ("+err.Error()+") but completed.")
		err = nil
	}
	if err != nil && errors.Is(err, errChanged) && !renamedBackup {
		// Another program replaced the file after the backup link. Leave
		// its file alone; the backup holds the version JellyTrim started
		// from and expires with the other backups.
		diag.Error = err.Error()
		p.removePartial(ctx, j.ID, partial, &diag)
		return Result{Outcome: Skipped, Diagnostics: diag, BackupPath: backup, Summary: fmt.Sprintf(
			"Another program replaced the file while JellyTrim was working, so JellyTrim left that new file alone. The version JellyTrim started from is kept at %s until backups expire.", backup)}
	}
	if err != nil {
		diag.Error = err.Error()
		return p.abandonReplace(ctx, j, partial, backup, renamedBackup, diag)
	}
	_ = p.Journal.JournalComplete(ctx, entry)
	if err := p.FS.SyncDir(filepath.Dir(j.Path)); err != nil {
		diag.Warnings = append(diag.Warnings, "Could not flush the folder to disk: "+err.Error())
	}
	newInfo, err := p.FS.Stat(j.Path)
	if err != nil {
		diag.Warnings = append(diag.Warnings, "Could not read the new file's details: "+err.Error())
	}
	return Result{Outcome: Complete, Summary: "Optimised.", Diagnostics: diag, OutputSize: checked.file.Size, Output: newInfo, BackupPath: backup}
}

// readyPartial re-checks the original is the file the job started on and
// the partial is the file that passed validation, then gives the partial
// the original's mode and owner and flushes it. It returns the partial's
// identity, or an outcome and summary to stop with.
func (p *Pipeline) readyPartial(j Job, partial string, checked fileid.Info, diag *Diagnostics) (fileid.Info, Outcome, string) {
	cur, err := p.FS.Stat(j.Path)
	if err != nil || !cur.Same(j.Identity) || cur.Nlink > 1 {
		return fileid.Info{}, Skipped, "The original changed while JellyTrim was working on it, so the new file was discarded. The original is unchanged."
	}
	// A long wait for playback leaves the checked file sitting on disk.
	// Only the exact file that passed validation may replace the original.
	if now, err := p.FS.Stat(partial); err != nil || !now.Same(checked) || now.IsSymlink || now.Nlink > 1 {
		diag.Error = "the new file is not the one that passed validation"
		if err != nil {
			diag.Error += ": " + err.Error()
		}
		return fileid.Info{}, Failed, "The new file changed after JellyTrim checked it, so it was discarded. The original is unchanged."
	}
	if err := p.FS.Chmod(partial, cur.Mode.Perm()); err != nil {
		diag.Error = err.Error()
		return fileid.Info{}, Failed, "JellyTrim could not give the new file the original's permissions, so it was discarded. The original is unchanged."
	}
	if err := p.FS.Chown(partial, cur.UID, cur.GID); err != nil {
		diag.Warnings = append(diag.Warnings, "The new file could not be given the original's owner ("+err.Error()+"). Check that Jellyfin can still read it.")
	}
	if err := p.FS.SyncFile(partial); err != nil {
		diag.Error = err.Error()
		return fileid.Info{}, Failed, "The new file could not be written to disk safely, so it was discarded. The original is unchanged."
	}
	partialInfo, err := p.FS.Stat(partial)
	if err != nil {
		diag.Error = err.Error()
		return fileid.Info{}, Failed, "The new file could not be read before moving it into place, so it was discarded. The original is unchanged."
	}
	return partialInfo, "", ""
}

// beforeReplace calls the job's BeforeReplace hook, which waits until
// nobody is playing the file and confirms the job should still go ahead. It
// runs before the identity re-check, so a file that changed during a long
// wait is still caught. When the hook says stop, the partial file is
// removed and the job ends with the hook's outcome (Interrupted for a plain
// error). If the partial cannot be removed, the job fails instead of going
// back to the queue, so the stray file is not forgotten.
func (p *Pipeline) beforeReplace(ctx context.Context, j Job, partial string, h Hooks, diag *Diagnostics) (Result, bool) {
	if j.BeforeReplace == nil {
		return Result{}, true
	}
	warning, err := j.BeforeReplace(ctx)
	h.note("")
	if warning != "" {
		diag.Warnings = append(diag.Warnings, warning)
	}
	if err == nil {
		return Result{}, true
	}
	outcome, summary := stopReason(ctx, err, diag)
	if rmErr := p.tryRemovePartial(context.WithoutCancel(ctx), j.ID, partial, diag); rmErr != nil {
		diag.Error += "; removing the new file: " + rmErr.Error()
		return Result{Outcome: Failed, Diagnostics: *diag, Summary: fmt.Sprintf(
			"The new file was not used, but JellyTrim could not delete it. Delete %s yourself. The original is unchanged.", partial)}, false
	}
	return Result{Outcome: outcome, Summary: summary, Diagnostics: *diag}, false
}

// stopReason turns BeforeReplace's error into the job's outcome and summary.
func stopReason(ctx context.Context, err error, diag *Diagnostics) (Outcome, string) {
	var stop *StopError
	switch {
	case ctx.Err() != nil:
		diag.Error = "stopped before replacing: " + ctx.Err().Error()
		return Interrupted, "Stopped before finishing. The original is unchanged."
	case errors.As(err, &stop):
		diag.Error = "not replaced: " + stop.Summary
		return stop.Outcome, stop.Summary
	}
	diag.Error = "waiting for playback to end: " + err.Error()
	return Interrupted, err.Error() + " The original is unchanged."
}

// abandonReplace undoes the backup after the final rename failed. If the
// undo fails too, the original may only exist under the backup name, so the
// job is left for attention rather than reported as unchanged.
func (p *Pipeline) abandonReplace(ctx context.Context, j Job, partial, backup string, renamed bool, diag Diagnostics) Result {
	if err := p.undoBackup(ctx, j, backup, renamed, &diag); err != nil {
		diag.Warnings = append(diag.Warnings, "Undoing the backup failed: "+err.Error())
		if !p.FS.Exists(j.Path) {
			// Keep the partial too: nothing is deleted while the original
			// is displaced. Recovery on the next start tries again.
			return Result{Outcome: NeedsAttention, Diagnostics: diag, BackupPath: backup, Summary: fmt.Sprintf(
				"The new file could not be moved into place, and JellyTrim could not put the original back. The original is safe at %s. JellyTrim will try again when it restarts; you can also rename it back yourself.", backup)}
		}
	}
	p.removePartial(ctx, j.ID, partial, &diag)
	return Result{Outcome: Failed, Diagnostics: diag,
		Summary: "The new file could not be moved into place, so JellyTrim put everything back. The original is unchanged."}
}

// backup keeps the original under the backup name: a hard link where the
// filesystem allows it (no copying, no extra space), otherwise a rename.
// It then checks the backup really is the file the job started on.
func (p *Pipeline) backup(ctx context.Context, j Job, backup string, diag *Diagnostics) (renamed bool, err error) {
	entry, err := p.Journal.JournalRecord(ctx, j.ID, StepBackupLinked, backup, j.Path)
	if err != nil {
		return false, err
	}
	linkErr := p.FS.Link(j.Path, backup)
	if linkErr == nil {
		_ = p.Journal.JournalComplete(ctx, entry)
	} else {
		diag.Warnings = append(diag.Warnings, "Hard links are not supported here ("+linkErr.Error()+"); the backup was made by renaming.")
		entry, err = p.Journal.JournalRecord(ctx, j.ID, StepBackupRenamed, backup, j.Path)
		if err != nil {
			return false, err
		}
		if err := p.FS.Rename(j.Path, backup); err != nil {
			return false, err
		}
		_ = p.Journal.JournalComplete(ctx, entry)
		renamed = true
	}
	// Something could have replaced the path between the check and the
	// link. The backup must be the original, or it is not a backup.
	if b, err := p.FS.Stat(backup); err != nil || !sameFile(b, j.Identity) {
		_ = p.undoBackup(ctx, j, backup, renamed, diag)
		return renamed, errChanged
	}
	return renamed, nil
}

// renameChecked renames the partial over the original, retrying while the
// target is busy. Before every attempt it checks the path still holds the
// original (link backup) or nothing (rename backup), so a file another
// program put there is never overwritten. Cross-device errors return at
// once: JellyTrim never copies and deletes.
func (p *Pipeline) renameChecked(j Job, partial string, renamedBackup bool) error {
	var err error
	for attempt := 0; attempt <= busyRetries; attempt++ {
		cur, statErr := p.FS.Stat(j.Path)
		switch {
		case renamedBackup && statErr == nil:
			return fmt.Errorf("%w: a file appeared at %s", errChanged, j.Path)
		case !renamedBackup && (statErr != nil || !sameFile(cur, j.Identity)):
			return fmt.Errorf("%w: %s is no longer the original", errChanged, j.Path)
		}
		err = p.FS.Rename(partial, j.Path)
		if err == nil || isCrossDevice(err) || !isBusy(err) {
			return err
		}
		p.sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
	}
	return err
}

// renameHappened reports whether the path now holds the partial file.
func (p *Pipeline) renameHappened(path string, partial fileid.Info) bool {
	cur, err := p.FS.Stat(path)
	return err == nil && cur.Dev == partial.Dev && cur.Inode == partial.Inode && partial.Inode != 0
}

// sameFile compares device and inode (and size), ignoring mtime and link
// count, which linking and renaming leave alone or change.
func sameFile(a, b fileid.Info) bool {
	return a.Dev == b.Dev && a.Inode == b.Inode && a.Size == b.Size
}

// undoBackup reverses the backup step, retrying a few times.
func (p *Pipeline) undoBackup(ctx context.Context, j Job, backup string, renamed bool, diag *Diagnostics) error {
	entry, _ := p.Journal.JournalRecord(ctx, j.ID, StepUndone, backup, j.Path)
	var err error
	for attempt := 0; attempt <= busyRetries; attempt++ {
		err = p.undoOnce(j, backup, renamed)
		if err == nil {
			if entry != 0 {
				_ = p.Journal.JournalComplete(ctx, entry)
			}
			return nil
		}
		p.sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
	}
	diag.Warnings = append(diag.Warnings, "Undoing the backup failed ("+err.Error()+"). The original is kept at "+backup+".")
	p.log().Error("pipeline: undoing backup", "backup", backup, "path", j.Path, "err", err)
	return err
}

func (p *Pipeline) undoOnce(j Job, backup string, renamed bool) error {
	if !p.FS.Exists(backup) {
		return nil
	}
	if renamed {
		if p.FS.Exists(j.Path) {
			return fmt.Errorf("%w: something is at %s, so the original was left at %s", errChanged, j.Path, backup)
		}
		// The original only exists under the backup name: put it back.
		return p.FS.Rename(backup, j.Path)
	}
	// A hard link: drop it only if the original is still in place.
	if cur, err := p.FS.Stat(j.Path); err == nil {
		if b, err := p.FS.Stat(backup); err == nil && cur.Dev == b.Dev && cur.Inode == b.Inode {
			return p.FS.Remove(backup)
		}
	}
	return nil
}

func (p *Pipeline) sleep(d time.Duration) {
	if p.Sleep != nil {
		p.Sleep(d)
		return
	}
	time.Sleep(d)
}
