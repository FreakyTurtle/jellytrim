package pipeline

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/freakyturtle/jellytrim/internal/fileid"

	"github.com/freakyturtle/jellytrim/internal/store"
)

// RecoveryState says what recovery found for an interrupted job.
type RecoveryState string

// Recovery states.
const (
	// RecoveredNothing: the job never touched the disk, or cleaned up itself.
	RecoveredNothing RecoveryState = "nothing"
	// RecoveredCleaned: a partial file (and any extra backup link) was
	// removed; the original is in place and the job can run again.
	RecoveredCleaned RecoveryState = "cleaned"
	// RecoveredRestored: the original had been moved to the backup name and
	// was put back.
	RecoveredRestored RecoveryState = "restored"
	// RecoveredReplaced: the replacement had finished; the new file is in
	// place and the backup holds the original.
	RecoveredReplaced RecoveryState = "replaced"
	// RecoveredNeedsAttention: the original was moved to the backup name and
	// something else now occupies its path, so JellyTrim cannot put it back
	// without destroying that file. BackupPath says where the original is.
	RecoveredNeedsAttention RecoveryState = "attention"
)

// Recovery is the result of recovering one job.
type Recovery struct {
	State      RecoveryState
	BackupPath string // when Replaced
	Actions    []string
}

// ErrUnsafePath is returned when a journal path does not look like one
// JellyTrim created next to the original. Recovery then does nothing.
var ErrUnsafePath = errors.New("journal path is not a JellyTrim file next to the original")

// Recover inspects an interrupted job's journal and puts the disk back into
// a safe state. It acts only on the exact paths in the journal, never on a
// pattern, and never deletes the original or its only copy.
func (p *Pipeline) Recover(ctx context.Context, jobID int64, original string) (Recovery, error) {
	var rec Recovery
	entries, err := p.Journal.JournalEntries(ctx, jobID)
	if err != nil {
		return rec, err
	}
	if len(entries) == 0 {
		rec.State = RecoveredNothing
		return rec, nil
	}
	for _, e := range entries {
		if err := checkJournalPath(e, original, jobID); err != nil {
			return rec, err
		}
	}
	if done, backup := p.replacementFinished(entries); done {
		rec.State, rec.BackupPath = RecoveredReplaced, backup
		return rec, nil
	}
	rec.State = RecoveredNothing
	if err := p.recoverBackup(ctx, jobID, entries, original, &rec); err != nil {
		return rec, err
	}
	for _, e := range entries {
		if e.Step == StepPartialCreated && p.FS.Exists(e.Path) {
			if err := p.journalled(ctx, jobID, StepPartialDeleted, e.Path, "", func() error { return p.FS.Remove(e.Path) }); err != nil {
				return rec, fmt.Errorf("removing partial file: %w", err)
			}
			rec.Actions = append(rec.Actions, "removed the partial file "+e.Path)
			if rec.State == RecoveredNothing {
				rec.State = RecoveredCleaned
			}
		}
	}
	return rec, nil
}

// replacementFinished reports whether the rename over the original happened.
func (p *Pipeline) replacementFinished(entries []store.JournalEntry) (bool, string) {
	backup := ""
	replaced := false
	for _, e := range entries {
		switch e.Step {
		case StepBackupLinked, StepBackupRenamed:
			backup = e.Path
		case StepReplaced:
			// Unmarked: the rename ran only if the partial is gone and the
			// path holds a different file from the backup (the original).
			replaced = e.CompletedAt != nil || (!p.FS.Exists(e.OtherPath) && p.differentFiles(e.Path, backup))
		case StepUndone, StepPartialDeleted:
			// Undoing, or deleting the partial afterwards, means the rename
			// did not happen (a successful rename leaves no partial).
			replaced = false
		}
	}
	return replaced, backup
}

// recoverBackup reverses a backup made for a replacement that did not finish.
func (p *Pipeline) recoverBackup(ctx context.Context, jobID int64, entries []store.JournalEntry, original string, rec *Recovery) error {
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.Step != StepBackupLinked && e.Step != StepBackupRenamed {
			continue
		}
		backup := e.Path
		switch {
		case !p.FS.Exists(backup):
			return nil // the link or rename never happened
		case !p.FS.Exists(original):
			// The original only exists under the backup name.
			if err := p.journalled(ctx, jobID, StepRestored, original, backup, func() error { return p.FS.Rename(backup, original) }); err != nil {
				return fmt.Errorf("restoring the original from %s: %w", backup, err)
			}
			rec.State = RecoveredRestored
			rec.Actions = append(rec.Actions, "restored the original from "+backup)
		default:
			// Both exist. Remove the backup only if it is the same file
			// (an extra hard link), never a different one.
			a, errA := p.FS.Stat(original)
			b, errB := p.FS.Stat(backup)
			if errA != nil || errB != nil || a.Dev != b.Dev || a.Inode != b.Inode {
				rec.Actions = append(rec.Actions, "kept "+backup+" because it is not the same file as the one at the original path")
				if e.Step == StepBackupRenamed {
					rec.State, rec.BackupPath = RecoveredNeedsAttention, backup
				}
				return nil
			}
			if err := p.journalled(ctx, jobID, StepBackupDeleted, backup, original, func() error { return p.FS.Remove(backup) }); err != nil {
				return fmt.Errorf("removing the extra backup link: %w", err)
			}
			rec.State = RecoveredCleaned
			rec.Actions = append(rec.Actions, "removed the unused backup link "+backup)
		}
		return nil
	}
	return nil
}

// differentFiles reports whether both paths exist and are different files.
func (p *Pipeline) differentFiles(a, b string) bool {
	if b == "" {
		return false
	}
	x, errA := p.FS.Stat(a)
	y, errB := p.FS.Stat(b)
	return errA == nil && errB == nil && (x.Dev != y.Dev || x.Inode != y.Inode)
}

// checkJournalPath refuses to act on anything other than the original and
// this job's own partial and backup names next to it.
func checkJournalPath(e store.JournalEntry, original string, jobID int64) error {
	allowed := map[string]bool{original: true, PartialPath(original, jobID): true, BackupPath(original, jobID): true}
	for _, p := range []string{e.Path, e.OtherPath} {
		if p != "" && !allowed[p] {
			return fmt.Errorf("%w: %s", ErrUnsafePath, p)
		}
	}
	return nil
}

// journalled records a step, runs it, and marks it complete.
func (p *Pipeline) journalled(ctx context.Context, jobID int64, step, path, other string, fn func() error) error {
	entry, err := p.Journal.JournalRecord(ctx, jobID, step, path, other)
	if err != nil {
		return err
	}
	if err := fn(); err != nil {
		return err
	}
	return p.Journal.JournalComplete(ctx, entry)
}

// DeleteBackup removes a completed job's backup of the original.
func (p *Pipeline) DeleteBackup(ctx context.Context, jobID int64, backup, original string) error {
	if err := checkJournalPath(store.JournalEntry{Path: backup}, original, jobID); err != nil {
		return err
	}
	if !p.FS.Exists(backup) {
		return nil
	}
	return p.journalled(ctx, jobID, StepBackupDeleted, backup, original, func() error { return p.FS.Remove(backup) })
}

// ErrNotOurFile is returned when the file at the path is no longer the one
// JellyTrim produced, so restoring would destroy someone else's file.
var ErrNotOurFile = errors.New("the file has changed since JellyTrim optimised it (for example a new download replaced it), so restoring would delete that file")

// Restore puts a completed job's original back, replacing the optimised
// file. output is the optimised file's identity recorded when the job
// finished; if the path no longer holds that file, nothing is changed.
func (p *Pipeline) Restore(ctx context.Context, jobID int64, backup, original string, output fileid.Info) error {
	if err := checkJournalPath(store.JournalEntry{Path: backup}, original, jobID); err != nil {
		return err
	}
	if !p.FS.Exists(backup) {
		return errors.New("the backup no longer exists")
	}
	if output.Inode == 0 {
		return ErrNotOurFile
	}
	if cur, err := p.FS.Stat(original); err == nil && !sameFile(cur, output) {
		return ErrNotOurFile
	}
	if err := p.journalled(ctx, jobID, StepRestored, original, backup, func() error { return p.FS.Rename(backup, original) }); err != nil {
		return err
	}
	if err := p.FS.SyncDir(filepath.Dir(original)); err != nil {
		p.log().Warn("pipeline: flushing folder after restore", "err", err)
	}
	return nil
}
