package store

import (
	"context"
	"database/sql"
	"fmt"
)

// Evaluating the whole library is a run: BeginEvaluations starts it,
// WriteEvaluations saves each batch as it is decided, and FinishEvaluations
// deletes the evaluations the run did not write. Every row keeps the ID of
// the run that last wrote it.
//
// A reader never sees an item lose its evaluation part-way: a batch replaces
// rows in place, and rows are deleted only when the run finishes. A run that
// stops early (an error, or the context ends) leaves the rows it did not
// reach as they were, and the next run that finishes removes whatever is
// stale. While a run is going a reader may see some items decided by it and
// others by the run before, which is what a slow run did before too.

// BeginEvaluations starts an evaluation run and returns its ID.
func (s *Store) BeginEvaluations(ctx context.Context) (int64, error) {
	var id int64
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var err error
		id, err = beginRun(ctx, tx, s.unix())
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("starting an evaluation run: %w", err)
	}
	return id, nil
}

// WriteEvaluations saves a batch of evaluations for run runID, replacing
// each item's previous one, in one short transaction. An evaluation for an
// item that has been removed since is dropped.
func (s *Store) WriteEvaluations(ctx context.Context, runID int64, evs []Evaluation) error {
	if len(evs) == 0 {
		return nil
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		return upsertEvaluations(ctx, tx, runID, s.unix(), evs)
	})
}

// FinishEvaluations ends run runID: it deletes every evaluation an earlier
// run wrote and this one did not, so items the run left without a decision
// (pending, or no longer managed) have none. It returns how many it deleted.
func (s *Store) FinishEvaluations(ctx context.Context, runID int64) (int64, error) {
	var n int64
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var err error
		n, err = finishRun(ctx, tx, runID, s.unix())
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("finishing evaluation run %d: %w", runID, err)
	}
	return n, nil
}

func beginRun(ctx context.Context, tx *sql.Tx, now int64) (int64, error) {
	res, err := tx.ExecContext(ctx, `INSERT INTO evaluation_runs (started_at) VALUES (?)`, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func upsertEvaluations(ctx context.Context, tx *sql.Tx, runID, now int64, evs []Evaluation) error {
	// INSERT ... SELECT needs a WHERE clause before ON CONFLICT, which the
	// check that the item still exists provides.
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO evaluations (item_id, outcome, policy_id, summary, explanation,
		reasons, plan, est_min_bytes, est_max_bytes, evaluated_at, run_id)
		SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM items WHERE id = ?)
		ON CONFLICT(item_id) DO UPDATE SET outcome = excluded.outcome, policy_id = excluded.policy_id,
		summary = excluded.summary, explanation = excluded.explanation, reasons = excluded.reasons,
		plan = excluded.plan, est_min_bytes = excluded.est_min_bytes, est_max_bytes = excluded.est_max_bytes,
		evaluated_at = excluded.evaluated_at, run_id = excluded.run_id`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()
	for _, e := range evs {
		if _, err := stmt.ExecContext(ctx, e.ItemID, e.Outcome, e.PolicyID, e.Summary, e.Explanation, e.Reasons,
			e.Plan, e.EstMin, e.EstMax, now, runID, e.ItemID); err != nil {
			return fmt.Errorf("saving evaluation for %s: %w", e.ItemID, err)
		}
	}
	return nil
}

// finishRun uses "older than this run" rather than "not this run", so a run
// that finishes after a newer one started cannot delete the newer one's rows.
func finishRun(ctx context.Context, tx *sql.Tx, runID, now int64) (int64, error) {
	res, err := tx.ExecContext(ctx, `DELETE FROM evaluations WHERE run_id < ?`, runID)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE evaluation_runs SET finished_at = ? WHERE id = ?`, now, runID); err != nil {
		return 0, err
	}
	// Older runs are finished or abandoned; their rows are gone.
	if _, err := tx.ExecContext(ctx, `DELETE FROM evaluation_runs WHERE id < ?`, runID); err != nil {
		return 0, err
	}
	return n, nil
}
