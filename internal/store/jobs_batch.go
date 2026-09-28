package store

import (
	"context"
	"database/sql"
	"fmt"
)

// createBatch is how many jobs CreateJobs inserts per transaction. Each
// transaction holds the write lock, so a batch is kept short enough not to
// hold up journal writes from running jobs.
const createBatch = 2000

// jobBlockers are the items and files that already have an active job or
// one that needs attention.
type jobBlockers struct {
	items map[string]bool
	paths map[string]bool
	maxID int64 // the highest job ID read so far
}

// load adds blockers from active jobs newer than the last load. Other
// writers can only add jobs between batches, and new jobs have higher IDs.
// A job that has finished since stays in the set, which only means its item
// waits for the next round of queueing. The jobs_one_active index still
// stops a second active job for an item in any case.
func (b *jobBlockers) load(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT id, item_id, local_path FROM jobs WHERE id > ?
		AND status IN ('waiting', 'analysing', 'encoding', 'validating', 'replacing', 'attention')`, b.maxID)
	if err != nil {
		return fmt.Errorf("reading queued jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var item, path string
		if err := rows.Scan(&id, &item, &path); err != nil {
			return err
		}
		b.add(item, path)
		b.maxID = max(b.maxID, id)
	}
	return rows.Err()
}

func (b *jobBlockers) add(item, path string) {
	b.items[item] = true
	if path != "" {
		b.paths[path] = true
	}
}

func (b *jobBlockers) blocks(j Job) bool {
	return b.items[j.ItemID] || (j.LocalPath != "" && b.paths[j.LocalPath])
}

// CreateJobs queues many jobs with the same rules as CreateJob: no job for
// an item or file that already has an active job or one that needs
// attention, and at most one per item or file within the list. It returns
// how many were created.
func (s *Store) CreateJobs(ctx context.Context, jobs []Job) (int, error) {
	b := &jobBlockers{items: map[string]bool{}, paths: map[string]bool{}}
	created := 0
	for start := 0; start < len(jobs); start += createBatch {
		n, err := s.createJobBatch(ctx, b, jobs[start:min(start+createBatch, len(jobs))])
		if err != nil {
			return created, err
		}
		created += n
	}
	return created, nil
}

func (s *Store) createJobBatch(ctx context.Context, b *jobBlockers, jobs []Job) (int, error) {
	// Jobs added in this batch count only once it commits.
	batch := &jobBlockers{items: map[string]bool{}, paths: map[string]bool{}}
	err := s.tx(ctx, func(tx *sql.Tx) error {
		if err := b.load(ctx, tx); err != nil {
			return err
		}
		stmt, err := tx.PrepareContext(ctx, insertJobSQL)
		if err != nil {
			return err
		}
		defer func() { _ = stmt.Close() }()
		now := s.unix()
		for _, j := range jobs {
			if b.blocks(j) || batch.blocks(j) {
				continue
			}
			_, err := stmt.ExecContext(ctx, jobInsertArgs(j, now)...)
			if isUniqueViolation(err) {
				continue
			}
			if err != nil {
				return fmt.Errorf("queueing job for %s: %w", j.ItemID, err)
			}
			batch.add(j.ItemID, j.LocalPath)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	for item := range batch.items {
		b.items[item] = true
	}
	for path := range batch.paths {
		b.paths[path] = true
	}
	return len(batch.items), nil
}
