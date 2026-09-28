package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Job statuses.
const (
	JobWaiting    = "waiting"
	JobAnalysing  = "analysing"
	JobEncoding   = "encoding"
	JobValidating = "validating"
	JobReplacing  = "replacing"
	JobComplete   = "complete"
	JobSkipped    = "skipped"
	JobFailed     = "failed"
	JobCancelled  = "cancelled"
	// JobAttention is a job that stopped in a state the user needs to look
	// at. It is not active, but no new job is queued for its item or file,
	// and recovery revisits it on every start.
	JobAttention = "attention"
)

// ActiveStatuses are statuses of jobs that are waiting or running.
var ActiveStatuses = []string{JobWaiting, JobAnalysing, JobEncoding, JobValidating, JobReplacing}

// Job is one queued or finished optimisation.
type Job struct {
	ID             int64
	ItemID         string
	ItemName       string
	LibraryName    string
	PolicyID       *int64
	PolicyName     string
	Status         string
	Trigger        string
	Plan           string // JSON plan.Plan
	LocalPath      string
	JellyfinPath   string
	SourceIdentity string // JSON FileIdentity
	OutputIdentity string // JSON FileIdentity of the file the job wrote
	SourceSummary  string
	TargetSummary  string
	SourceSize     *int64
	OutputSize     *int64
	EstMin         *int64
	EstMax         *int64
	// EstSaving is the conservative estimated saving: the source size
	// minus EstMax, never negative. The store sets it; the queue runs the
	// biggest savings first.
	EstSaving int64
	// DurationMs is the source's duration from its probe, 0 when unknown.
	DurationMs      int64
	Progress        float64
	Speed           float64
	ETASeconds      *int64
	Encoder         string
	Summary         string
	Diagnostics     string // JSON
	PartialPath     string
	BackupPath      string
	BackupExpiresAt *time.Time
	RestoredAt      *time.Time
	CreatedAt       time.Time
	StartedAt       *time.Time
	FinishedAt      *time.Time
}

// Active reports whether the job is waiting or running.
func (j Job) Active() bool {
	for _, s := range ActiveStatuses {
		if j.Status == s {
			return true
		}
	}
	return false
}

// ActiveJobCount counts jobs that are waiting or in progress.
func (s *Store) ActiveJobCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE status IN
		('waiting', 'analysing', 'encoding', 'validating', 'replacing')`).Scan(&n)
	return n, err
}

// CreateJob queues a job unless one is already queued or running for the
// same item or the same file (two Jellyfin items can share a file), or a job
// for either needs attention. It returns the job ID (the existing job's when
// none was created) and whether a new job was created.
//
// The transaction takes the write lock when it begins (_txlock=immediate),
// so the check and the insert cannot interleave with another writer. The
// jobs_one_active index backs this up for the item.
func (s *Store) CreateJob(ctx context.Context, j Job) (int64, bool, error) {
	var id int64
	created := false
	err := s.tx(ctx, func(tx *sql.Tx) error {
		existing, err := blockingJob(ctx, tx, j.ItemID, j.LocalPath)
		if err != nil || existing != 0 {
			id = existing
			return err
		}
		res, err := tx.ExecContext(ctx, insertJobSQL, jobInsertArgs(j, s.unix())...)
		if isUniqueViolation(err) {
			id, err = blockingJob(ctx, tx, j.ItemID, j.LocalPath)
			return err
		}
		if err != nil {
			return fmt.Errorf("queueing job: %w", err)
		}
		id, err = res.LastInsertId()
		created = true
		return err
	})
	return id, created, err
}

const insertJobSQL = `INSERT INTO jobs (item_id, item_name, library_name, policy_id, policy_name, status, trigger,
	plan, local_path, jellyfin_path, source_summary, target_summary, source_size, est_min_bytes, est_max_bytes,
	est_saving, duration_ms, created_at)
	VALUES (?, ?, ?, ?, ?, 'waiting', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

func jobInsertArgs(j Job, now int64) []any {
	return []any{j.ItemID, j.ItemName, j.LibraryName, j.PolicyID, j.PolicyName, orDefault(j.Trigger, "auto"), j.Plan,
		j.LocalPath, j.JellyfinPath, j.SourceSummary, j.TargetSummary, j.SourceSize, j.EstMin, j.EstMax,
		estSaving(j), max(j.DurationMs, 0), now}
}

// estSaving is the conservative saving: source size minus the largest
// estimated output. It is 0 when either is unknown.
func estSaving(j Job) int64 {
	if j.SourceSize == nil || j.EstMax == nil {
		return 0
	}
	return max(*j.SourceSize-*j.EstMax, 0)
}

// blockingJob returns the oldest job that stops a new one for the item or
// file: an active job, or one that needs attention. It returns 0 when there
// is none. The two halves use the jobs_item and jobs_local_path indexes; an
// OR across both columns would scan every job.
func blockingJob(ctx context.Context, tx *sql.Tx, itemID, localPath string) (int64, error) {
	var id sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT MIN(id) FROM (
		SELECT id FROM jobs WHERE item_id = ?
			AND status IN ('waiting', 'analysing', 'encoding', 'validating', 'replacing', 'attention')
		UNION ALL
		SELECT id FROM jobs WHERE local_path = ? AND local_path != ''
			AND status IN ('waiting', 'analysing', 'encoding', 'validating', 'replacing', 'attention'))`,
		itemID, localPath).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("checking for a queued job: %w", err)
	}
	return id.Int64, nil
}

func isUniqueViolation(err error) bool {
	var se *sqlite.Error
	return errors.As(err, &se) && se.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE
}

// ActiveJobForItem returns the item's waiting or running job, if it has one.
func (s *Store) ActiveJobForItem(ctx context.Context, itemID string) (Job, bool, error) {
	j, err := scanJob(s.db.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE item_id = ?
		AND status IN ('waiting', 'analysing', 'encoding', 'validating', 'replacing') ORDER BY id LIMIT 1`, itemID))
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, fmt.Errorf("reading the active job for %s: %w", itemID, err)
	}
	return j, true, nil
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

const jobColumns = `id, item_id, item_name, library_name, policy_id, policy_name, status, trigger, plan, local_path,
	jellyfin_path, source_identity, output_identity, source_summary, target_summary, source_size, output_size, est_min_bytes, est_max_bytes,
	est_saving, duration_ms, progress, speed, eta_seconds, encoder, summary, diagnostics, partial_path, backup_path, backup_expires_at, restored_at,
	created_at, started_at, finished_at`

func scanJob(r scanner) (Job, error) {
	var j Job
	var pid, src, out, lo, hi, eta, bexp, rest, started, finished sql.NullInt64
	var created int64
	err := r.Scan(&j.ID, &j.ItemID, &j.ItemName, &j.LibraryName, &pid, &j.PolicyName, &j.Status, &j.Trigger, &j.Plan,
		&j.LocalPath, &j.JellyfinPath, &j.SourceIdentity, &j.OutputIdentity, &j.SourceSummary, &j.TargetSummary, &src, &out, &lo, &hi,
		&j.EstSaving, &j.DurationMs, &j.Progress, &j.Speed, &eta, &j.Encoder, &j.Summary, &j.Diagnostics, &j.PartialPath, &j.BackupPath, &bexp, &rest,
		&created, &started, &finished)
	if err != nil {
		return j, err
	}
	j.PolicyID, j.SourceSize, j.OutputSize = int64Ptr(pid), int64Ptr(src), int64Ptr(out)
	j.EstMin, j.EstMax, j.ETASeconds = int64Ptr(lo), int64Ptr(hi), int64Ptr(eta)
	j.BackupExpiresAt, j.RestoredAt = timePtr(bexp), timePtr(rest)
	j.CreatedAt = time.Unix(created, 0).UTC()
	j.StartedAt, j.FinishedAt = timePtr(started), timePtr(finished)
	return j, nil
}

// Job loads one job, or ErrNotFound.
func (s *Store) Job(ctx context.Context, id int64) (Job, error) {
	j, err := scanJob(s.db.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return j, ErrNotFound
	}
	return j, err
}

func (s *Store) jobList(ctx context.Context, q string, args ...any) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+jobColumns+` FROM jobs `+q, args...) // #nosec G202 -- fixed clauses
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// waitingOrder is the order waiting jobs run in: jobs the user queued by
// hand first, then the biggest estimated saving, then the oldest. It must
// match the jobs_waiting_order index expressions.
const waitingOrder = `(trigger != 'manual'), est_saving DESC, id`

// ActiveJobs lists waiting and running jobs: running first (oldest first),
// then waiting jobs in the order they will run.
func (s *Store) ActiveJobs(ctx context.Context) ([]Job, error) {
	return s.jobList(ctx, `WHERE status IN ('waiting', 'analysing', 'encoding', 'validating', 'replacing')
		ORDER BY status = 'waiting', CASE WHEN status = 'waiting' THEN (trigger != 'manual') END,
		CASE WHEN status = 'waiting' THEN est_saving END DESC, id`)
}

// NextWaitingJob returns the waiting job that should run next, or
// ErrNotFound.
func (s *Store) NextWaitingJob(ctx context.Context) (Job, error) {
	jobs, err := s.NextWaitingJobs(ctx, 1)
	if err != nil {
		return Job{}, err
	}
	if len(jobs) == 0 {
		return Job{}, ErrNotFound
	}
	return jobs[0], nil
}

// NextWaitingJobs returns up to limit waiting jobs in the order they should
// run, so the dispatcher can pass over one that cannot start yet.
func (s *Store) NextWaitingJobs(ctx context.Context, limit int) ([]Job, error) {
	return s.jobList(ctx, `WHERE status = 'waiting' ORDER BY `+waitingOrder+` LIMIT ?`, limit) // #nosec G202 -- constant order
}

// HistoryJobs lists finished jobs, newest first.
func (s *Store) HistoryJobs(ctx context.Context, status string, limit, offset int) ([]Job, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	if status != "" {
		return s.jobList(ctx, `WHERE status = ? ORDER BY COALESCE(finished_at, created_at) DESC, id DESC LIMIT ? OFFSET ?`, status, limit, offset)
	}
	return s.jobList(ctx, `WHERE status IN ('complete', 'skipped', 'failed', 'cancelled')
		ORDER BY COALESCE(finished_at, created_at) DESC, id DESC LIMIT ? OFFSET ?`, limit, offset)
}

// JobsWithBackups lists completed jobs whose backup still exists.
func (s *Store) JobsWithBackups(ctx context.Context) ([]Job, error) {
	return s.jobList(ctx, `WHERE backup_path != '' ORDER BY id`)
}

// StartJob moves a waiting job to analysing and records its source identity.
// It returns false if the job was no longer waiting (cancelled meanwhile).
func (s *Store) StartJob(ctx context.Context, id int64, identity, encoder string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE jobs SET status = 'analysing', source_identity = ?, encoder = ?, started_at = ?,
		progress = 0, speed = 0, eta_seconds = NULL, summary = '' WHERE id = ? AND status = 'waiting'`, identity, encoder, s.unix(), id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// SetJobStatus changes a running job's status.
func (s *Store) SetJobStatus(ctx context.Context, id int64, status string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET status = ? WHERE id = ?`, status, id)
	return err
}

// SetJobPartial records the partial output path while encoding.
func (s *Store) SetJobPartial(ctx context.Context, id int64, partial string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET partial_path = ? WHERE id = ?`, partial, id)
	return err
}

// SetJobProgress stores encoding progress.
func (s *Store) SetJobProgress(ctx context.Context, id int64, progress, speed float64, eta *int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET progress = ?, speed = ?, eta_seconds = ? WHERE id = ?`, progress, speed, eta, id)
	return err
}

// JobOutcome is how a job finished.
type JobOutcome struct {
	Status          string
	Summary         string
	Diagnostics     string
	OutputSize      *int64
	BackupPath      string
	BackupExpiresAt *time.Time
	OutputIdentity  string // JSON FileIdentity; empty keeps the stored value
}

// FinishJob records a job's final state.
func (s *Store) FinishJob(ctx context.Context, id int64, o JobOutcome) error {
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET status = ?, summary = ?, diagnostics = ?, output_size = ?, backup_path = ?,
		backup_expires_at = ?, partial_path = '', finished_at = ?, eta_seconds = NULL,
		output_identity = CASE WHEN ? = '' THEN output_identity ELSE ? END WHERE id = ?`,
		o.Status, o.Summary, o.Diagnostics, o.OutputSize, o.BackupPath, unixPtr(o.BackupExpiresAt), s.unix(),
		o.OutputIdentity, o.OutputIdentity, id)
	return err
}

// SetJobOutputIdentity records the identity (JSON FileIdentity) of the file
// a job wrote, as soon as it is known.
func (s *Store) SetJobOutputIdentity(ctx context.Context, id int64, identityJSON string) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE jobs SET output_identity = ? WHERE id = ?`, identityJSON, id); err != nil {
		return fmt.Errorf("recording the output identity of job %d: %w", id, err)
	}
	return nil
}

// UpdateJobPaths records new local and Jellyfin paths for a job, for example
// after Jellyfin moved the item.
func (s *Store) UpdateJobPaths(ctx context.Context, id int64, localPath, jellyfinPath string) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE jobs SET local_path = ?, jellyfin_path = ? WHERE id = ?`,
		localPath, jellyfinPath, id); err != nil {
		return fmt.Errorf("updating the paths of job %d: %w", id, err)
	}
	return nil
}

// RequeueJob puts an interrupted or failed job back to waiting.
func (s *Store) RequeueJob(ctx context.Context, id int64, summary string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET status = 'waiting', summary = ?, progress = 0, speed = 0, eta_seconds = NULL,
		partial_path = '', started_at = NULL, finished_at = NULL WHERE id = ?`, summary, id)
	return err
}

// CancelJob cancels a waiting job. It returns false if the job was not waiting.
func (s *Store) CancelJob(ctx context.Context, id int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE jobs SET status = 'cancelled', summary = 'Cancelled before it started.', finished_at = ?
		WHERE id = ? AND status = 'waiting'`, s.unix(), id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// ClearBackup records that a job's backup has been deleted or restored.
func (s *Store) ClearBackup(ctx context.Context, id int64, restored bool) error {
	q := `UPDATE jobs SET backup_path = '', backup_expires_at = NULL WHERE id = ?`
	args := []any{id}
	if restored {
		q = `UPDATE jobs SET backup_path = '', backup_expires_at = NULL, restored_at = ? WHERE id = ?`
		args = []any{s.unix(), id}
	}
	_, err := s.db.ExecContext(ctx, q, args...)
	return err
}

// SavedBytes is the total space JellyTrim has saved with completed jobs that
// were not restored.
func (s *Store) SavedBytes(ctx context.Context) (int64, int, error) {
	var saved int64
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(source_size - output_size), 0), COUNT(*) FROM jobs
		WHERE status = 'complete' AND restored_at IS NULL AND output_size IS NOT NULL AND source_size IS NOT NULL`).Scan(&saved, &n)
	return saved, n, err
}

// JournalEntry is one intended filesystem step of a job.
type JournalEntry struct {
	ID          int64
	JobID       int64
	Step        string
	Path        string
	OtherPath   string
	CreatedAt   time.Time
	CompletedAt *time.Time
}

// JournalRecord writes an intended step before it happens.
func (s *Store) JournalRecord(ctx context.Context, jobID int64, step, path, other string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO journal (job_id, step, path, other_path, created_at) VALUES (?, ?, ?, ?, ?)`,
		jobID, step, path, other, s.unix())
	if err != nil {
		return 0, fmt.Errorf("writing journal: %w", err)
	}
	return res.LastInsertId()
}

// JournalComplete marks a step as done.
func (s *Store) JournalComplete(ctx context.Context, entryID int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE journal SET completed_at = ? WHERE id = ?`, s.unix(), entryID)
	return err
}

// JournalEntries lists a job's journal in order.
func (s *Store) JournalEntries(ctx context.Context, jobID int64) ([]JournalEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, job_id, step, path, other_path, created_at, completed_at
		FROM journal WHERE job_id = ? ORDER BY id`, jobID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []JournalEntry
	for rows.Next() {
		var e JournalEntry
		var created int64
		var done sql.NullInt64
		if err := rows.Scan(&e.ID, &e.JobID, &e.Step, &e.Path, &e.OtherPath, &created, &done); err != nil {
			return nil, err
		}
		e.CreatedAt = time.Unix(created, 0).UTC()
		e.CompletedAt = timePtr(done)
		out = append(out, e)
	}
	return out, rows.Err()
}

// InterruptedJobs lists jobs left in a running status by a crash or restart,
// and jobs that need attention, which recovery revisits on every start.
func (s *Store) InterruptedJobs(ctx context.Context) ([]Job, error) {
	return s.jobList(ctx, `WHERE status IN ('analysing', 'encoding', 'validating', 'replacing', 'attention') ORDER BY id`)
}

// JobsNeedingAttention lists jobs in the attention status, oldest first.
func (s *Store) JobsNeedingAttention(ctx context.Context) ([]Job, error) {
	return s.jobList(ctx, `WHERE status = 'attention' ORDER BY id`)
}

// ItemsWithRecentJobs returns items that had a job created since the given
// time (any status), so automatic queueing does not retry failures in a loop.
func (s *Store) ItemsWithRecentJobs(ctx context.Context, since time.Time) (map[string]bool, error) {
	// Jobs skipped before encoding (no encoder yet, file changing) cost
	// nothing to retry, so they do not hold an item back.
	// No DISTINCT: it would make SQLite walk jobs_item instead of the
	// jobs_created range; the map removes duplicates.
	rows, err := s.db.QueryContext(ctx, `SELECT item_id FROM jobs WHERE created_at >= ?
		AND NOT (status = 'skipped' AND json_valid(diagnostics) AND json_extract(diagnostics, '$.step') = 'analysing')`, since.UTC().Unix())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// RecentJobs lists the latest finished jobs for the dashboard.
func (s *Store) RecentJobs(ctx context.Context, limit int) ([]Job, error) {
	return s.jobList(ctx, `WHERE status IN ('complete', 'skipped', 'failed') ORDER BY finished_at DESC, id DESC LIMIT ?`, limit)
}

// SetJobStatusDetail records the status, source identity and encoder label
// once analysis has picked them.
func (s *Store) SetJobStatusDetail(ctx context.Context, id int64, status, identity, encoder string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET status = ?, source_identity = ?, encoder = ? WHERE id = ?`, status, identity, encoder, id)
	return err
}
