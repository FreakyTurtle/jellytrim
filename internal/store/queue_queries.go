package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// QueueCandidate is an item the policies chose to optimise, with everything
// needed to build its job without further lookups.
type QueueCandidate struct {
	ItemID       string
	ItemName     string
	SeriesName   string
	LibraryName  string
	LocalPath    string
	JellyfinPath string
	PolicyID     *int64
	PolicyName   string
	Plan         string // JSON plan.Plan
	EstMin       *int64
	EstMax       *int64
	DurationMs   int64 // from the probe; 0 when unknown
}

const candidateSelect = `SELECT e.item_id, i.name, i.series_name, COALESCE(l.name, ''), i.local_path, i.jellyfin_path,
	e.policy_id, COALESCE(pol.name, ''), e.plan, e.est_min_bytes, e.est_max_bytes, COALESCE(p.duration_ms, 0)
	FROM evaluations e JOIN items i ON i.id = e.item_id LEFT JOIN libraries l ON l.id = i.library_id
	LEFT JOIN policies pol ON pol.id = e.policy_id LEFT JOIN probes p ON p.item_id = e.item_id
	WHERE e.outcome = 'optimise' `

func scanCandidate(r scanner) (QueueCandidate, error) {
	var c QueueCandidate
	var pid, lo, hi sql.NullInt64
	err := r.Scan(&c.ItemID, &c.ItemName, &c.SeriesName, &c.LibraryName, &c.LocalPath, &c.JellyfinPath,
		&pid, &c.PolicyName, &c.Plan, &lo, &hi, &c.DurationMs)
	c.PolicyID, c.EstMin, c.EstMax = int64Ptr(pid), int64Ptr(lo), int64Ptr(hi)
	return c, err
}

// QueueCandidates lists every item in a managed library that the policies
// chose to optimise, in name order.
func (s *Store) QueueCandidates(ctx context.Context) ([]QueueCandidate, error) {
	rows, err := s.db.QueryContext(ctx, candidateSelect+`AND l.managed = 1 ORDER BY i.sort_name, i.id`)
	if err != nil {
		return nil, fmt.Errorf("listing items to queue: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []QueueCandidate
	for rows.Next() {
		c, err := scanCandidate(rows)
		if err != nil {
			return nil, fmt.Errorf("listing items to queue: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// QueueCandidateFor returns one item's candidate, or ErrNotFound when the
// policies did not choose to optimise it.
func (s *Store) QueueCandidateFor(ctx context.Context, itemID string) (QueueCandidate, error) {
	c, err := scanCandidate(s.db.QueryRowContext(ctx, candidateSelect+`AND e.item_id = ?`, itemID))
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	if err != nil {
		return c, fmt.Errorf("reading %s to queue: %w", itemID, err)
	}
	return c, nil
}

// WaitingGroup totals waiting jobs that share an encoder.
type WaitingGroup struct {
	Encoder     string // the plan's encoder name, for example "x265"
	Jobs        int
	SourceBytes int64
	SavingMin   int64 // source size minus the largest estimate, never negative
	SavingMax   int64 // source size minus the smallest estimate, never negative
	DurationMs  int64 // total source duration of jobs whose duration is known
	NoDuration  int   // jobs whose duration is unknown
}

// WaitingByEncoder totals the waiting jobs per planned encoder.
func (s *Store) WaitingByEncoder(ctx context.Context) ([]WaitingGroup, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT COALESCE(CASE WHEN json_valid(plan) THEN json_extract(plan, '$.encoder') END, ''),
		COUNT(*), COALESCE(SUM(source_size), 0), COALESCE(SUM(est_saving), 0),
		COALESCE(SUM(MAX(COALESCE(source_size, 0) - COALESCE(est_min_bytes, source_size, 0), 0)), 0),
		COALESCE(SUM(duration_ms), 0), SUM(duration_ms <= 0)
		FROM jobs WHERE status = 'waiting' GROUP BY 1 ORDER BY 1`)
	if err != nil {
		return nil, fmt.Errorf("totalling waiting jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []WaitingGroup
	for rows.Next() {
		var g WaitingGroup
		if err := rows.Scan(&g.Encoder, &g.Jobs, &g.SourceBytes, &g.SavingMin, &g.SavingMax, &g.DurationMs, &g.NoDuration); err != nil {
			return nil, fmt.Errorf("totalling waiting jobs: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// RecentEncodeSpeeds returns the encode speed (a multiple of real time)
// recorded in the diagnostics of the latest limit complete jobs that have
// one, newest first.
func (s *Store) RecentEncodeSpeeds(ctx context.Context, limit int) ([]float64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT speed FROM (
		SELECT CASE WHEN json_valid(diagnostics) THEN json_extract(diagnostics, '$.speed') END AS speed, finished_at, id
		FROM jobs WHERE status = 'complete') WHERE speed > 0 ORDER BY finished_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("reading encode speeds: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []float64
	for rows.Next() {
		var v float64
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("reading encode speeds: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// WaitingSpace is what the space outlook needs to know about a waiting
// job.
type WaitingSpace struct {
	JobID     int64
	LocalPath string
	EstMax    int64 // 0 when unknown
	// Dev is the device of the item's file from its cached probe, and
	// DevKnown is false when there is no probe.
	Dev      uint64
	DevKnown bool
}

// WaitingJobSpace lists every waiting job's path, largest estimated output
// and the device its file was last seen on.
func (s *Store) WaitingJobSpace(ctx context.Context) ([]WaitingSpace, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT j.id, j.local_path, COALESCE(j.est_max_bytes, 0), p.dev
		FROM jobs j LEFT JOIN probes p ON p.item_id = j.item_id AND p.local_path = j.local_path
		WHERE j.status = 'waiting' ORDER BY j.id`)
	if err != nil {
		return nil, fmt.Errorf("reading waiting jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []WaitingSpace
	for rows.Next() {
		var w WaitingSpace
		var dev sql.NullInt64
		if err := rows.Scan(&w.JobID, &w.LocalPath, &w.EstMax, &dev); err != nil {
			return nil, fmt.Errorf("reading waiting jobs: %w", err)
		}
		w.Dev, w.DevKnown = uint64(dev.Int64), dev.Valid
		out = append(out, w)
	}
	return out, rows.Err()
}
