package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Evaluation is the latest decision for one item. JSON fields are owned by
// internal/policy and internal/plan.
type Evaluation struct {
	ItemID      string
	Outcome     string
	PolicyID    *int64
	Summary     string
	Explanation string // JSON []policy.Match
	Reasons     string // JSON []plan.Reason
	Plan        string // JSON plan.Plan, or ""
	EstMin      *int64
	EstMax      *int64
	EvaluatedAt time.Time
}

// ReplaceEvaluations stores a complete set of evaluations, removing any for
// items not in the set.
func (s *Store) ReplaceEvaluations(ctx context.Context, evs []Evaluation) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM evaluations`); err != nil {
			return err
		}
		stmt, err := tx.PrepareContext(ctx, `INSERT INTO evaluations (item_id, outcome, policy_id, summary, explanation, reasons,
			plan, est_min_bytes, est_max_bytes, evaluated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer func() { _ = stmt.Close() }()
		now := s.unix()
		for _, e := range evs {
			if _, err := stmt.ExecContext(ctx, e.ItemID, e.Outcome, e.PolicyID, e.Summary, e.Explanation, e.Reasons,
				e.Plan, e.EstMin, e.EstMax, now); err != nil {
				return fmt.Errorf("saving evaluation for %s: %w", e.ItemID, err)
			}
		}
		return nil
	})
}

// Evaluation returns an item's latest evaluation, or ErrNotFound.
func (s *Store) Evaluation(ctx context.Context, itemID string) (Evaluation, error) {
	var e Evaluation
	var pid, lo, hi sql.NullInt64
	var at int64
	err := s.db.QueryRowContext(ctx, `SELECT item_id, outcome, policy_id, summary, explanation, reasons, plan,
		est_min_bytes, est_max_bytes, evaluated_at FROM evaluations WHERE item_id = ?`, itemID).
		Scan(&e.ItemID, &e.Outcome, &pid, &e.Summary, &e.Explanation, &e.Reasons, &e.Plan, &lo, &hi, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	e.PolicyID = int64Ptr(pid)
	e.EstMin, e.EstMax = int64Ptr(lo), int64Ptr(hi)
	e.EvaluatedAt = time.Unix(at, 0).UTC()
	return e, err
}

func int64Ptr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

// LibraryFilter narrows the Library list. Empty fields mean "any".
type LibraryFilter struct {
	Outcome    string // optimise, optimal, protected, skipped, no_policy, pending
	Resolution int    // 2160, 1080, 720 (class)
	Codec      string
	HDR        bool
	Watched    string // "yes", "no"
	LibraryID  string
	Search     string
	Sort       string // "name" (default), "size", "saving"
	Limit      int
	Offset     int
}

// LibraryRow is one row of the Library list.
type LibraryRow struct {
	Item       Item
	Size       int64
	VideoCodec string
	Width      int
	Height     int
	Resolution int
	HDR        string
	Probed     bool
	ProbeError string
	Outcome    string
	Summary    string
	EstMin     *int64
	EstMax     *int64
	Watched    bool
	Favourite  bool
}

// LibraryList returns managed items with their probe summary and evaluation,
// plus the total count before paging.
func (s *Store) LibraryList(ctx context.Context, f LibraryFilter) ([]LibraryRow, int, error) {
	where, args := libraryWhere(f)
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM items i JOIN libraries l ON l.id = i.library_id
		LEFT JOIN probes p ON p.item_id = i.id LEFT JOIN evaluations e ON e.item_id = i.id `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting library: %w", err)
	}
	order := "i.sort_name COLLATE NOCASE, i.id"
	switch f.Sort {
	case "size":
		order = "COALESCE(NULLIF(p.size, 0), i.jellyfin_size, 0) DESC, i.id"
	case "saving":
		order = "(COALESCE(p.size, 0) - COALESCE(e.est_max_bytes, COALESCE(p.size, 0))) DESC, i.id"
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT ` + itemColumns + `, COALESCE(NULLIF(p.size, 0), i.jellyfin_size, 0), COALESCE(p.video_codec, ''), COALESCE(p.width, 0),
		COALESCE(p.height, 0), COALESCE(p.resolution, 0), COALESCE(p.hdr, ''), p.item_id IS NOT NULL, COALESCE(p.error, ''),
		COALESCE(e.outcome, ''), COALESCE(e.summary, ''), e.est_min_bytes, e.est_max_bytes,
		EXISTS (SELECT 1 FROM item_user_data d JOIN jellyfin_users u ON u.id = d.user_id WHERE d.item_id = i.id AND u.selected = 1 AND u.disabled = 0 AND d.played = 1),
		EXISTS (SELECT 1 FROM item_user_data d JOIN jellyfin_users u ON u.id = d.user_id WHERE d.item_id = i.id AND u.selected = 1 AND u.disabled = 0 AND d.favorite = 1)
		FROM items i JOIN libraries l ON l.id = i.library_id
		LEFT JOIN probes p ON p.item_id = i.id LEFT JOIN evaluations e ON e.item_id = i.id ` + where +
		` ORDER BY ` + order + ` LIMIT ? OFFSET ?` // #nosec G202 -- order comes from a fixed switch
	rows, err := s.db.QueryContext(ctx, q, append(args, limit, max(f.Offset, 0))...)
	if err != nil {
		return nil, 0, fmt.Errorf("listing library: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []LibraryRow
	for rows.Next() {
		var r LibraryRow
		var lo, hi sql.NullInt64
		it, err := scanItem(rows, &r.Size, &r.VideoCodec, &r.Width, &r.Height, &r.Resolution, &r.HDR, &r.Probed,
			&r.ProbeError, &r.Outcome, &r.Summary, &lo, &hi, &r.Watched, &r.Favourite)
		if err != nil {
			return nil, 0, err
		}
		r.Item = it
		r.EstMin, r.EstMax = int64Ptr(lo), int64Ptr(hi)
		out = append(out, r)
	}
	return out, total, rows.Err()
}

func libraryWhere(f LibraryFilter) (string, []any) {
	conds := []string{"l.managed = 1"}
	var args []any
	switch f.Outcome {
	case "":
	case "pending":
		conds = append(conds, "e.item_id IS NULL")
	default:
		conds = append(conds, "e.outcome = ?")
		args = append(args, f.Outcome)
	}
	if f.Resolution > 0 {
		conds = append(conds, "p.resolution = ?")
		args = append(args, f.Resolution)
	}
	if f.Codec != "" {
		conds = append(conds, "p.video_codec = ?")
		args = append(args, f.Codec)
	}
	if f.HDR {
		conds = append(conds, "p.hdr NOT IN ('', 'sdr')")
	}
	watched := `EXISTS (SELECT 1 FROM item_user_data d JOIN jellyfin_users u ON u.id = d.user_id
		WHERE d.item_id = i.id AND u.selected = 1 AND u.disabled = 0 AND d.played = 1)`
	switch f.Watched {
	case "yes":
		conds = append(conds, watched)
	case "no":
		conds = append(conds, "NOT "+watched)
	}
	if f.LibraryID != "" {
		conds = append(conds, "i.library_id = ?")
		args = append(args, f.LibraryID)
	}
	if s := strings.TrimSpace(f.Search); s != "" {
		conds = append(conds, "(i.name LIKE ? ESCAPE '\\' OR i.series_name LIKE ? ESCAPE '\\')")
		like := "%" + escapeLike(s) + "%"
		args = append(args, like, like)
	}
	return "WHERE " + strings.Join(conds, " AND "), args
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// OutcomeTotals summarises evaluations for the dashboard and Dry Run.
type OutcomeTotals struct {
	Items       int
	ByOutcome   map[string]int
	SourceBytes map[string]int64 // current size per outcome
	EstMin      int64            // for optimise outcomes: estimated result range
	EstMax      int64
	TotalBytes  int64 // every managed item's current size
	Unprobed    int
}

// Totals computes outcome counts and sizes across managed items.
func (s *Store) Totals(ctx context.Context) (OutcomeTotals, error) {
	t := OutcomeTotals{ByOutcome: map[string]int{}, SourceBytes: map[string]int64{}}
	rows, err := s.db.QueryContext(ctx, `SELECT COALESCE(e.outcome, 'pending'), COUNT(*), COALESCE(SUM(COALESCE(NULLIF(p.size, 0), i.jellyfin_size, 0)), 0),
		COALESCE(SUM(e.est_min_bytes), 0), COALESCE(SUM(e.est_max_bytes), 0), SUM(p.item_id IS NULL)
		FROM items i JOIN libraries l ON l.id = i.library_id AND l.managed = 1
		LEFT JOIN probes p ON p.item_id = i.id LEFT JOIN evaluations e ON e.item_id = i.id
		GROUP BY 1`)
	if err != nil {
		return t, fmt.Errorf("totals: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var outcome string
		var n, unprobed int
		var size, lo, hi int64
		if err := rows.Scan(&outcome, &n, &size, &lo, &hi, &unprobed); err != nil {
			return t, err
		}
		t.Items += n
		t.ByOutcome[outcome] = n
		t.SourceBytes[outcome] = size
		t.TotalBytes += size
		t.Unprobed += unprobed
		if outcome == "optimise" {
			t.EstMin, t.EstMax = lo, hi
		}
	}
	return t, rows.Err()
}

// ItemsWithOutcome returns evaluations with the given outcome, for queueing.
func (s *Store) ItemsWithOutcome(ctx context.Context, outcome string) ([]Evaluation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT e.item_id, e.outcome, e.policy_id, e.summary, e.plan, e.est_min_bytes, e.est_max_bytes
		FROM evaluations e JOIN items i ON i.id = e.item_id JOIN libraries l ON l.id = i.library_id AND l.managed = 1
		WHERE e.outcome = ? ORDER BY i.sort_name`, outcome)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Evaluation
	for rows.Next() {
		var e Evaluation
		var pid, lo, hi sql.NullInt64
		if err := rows.Scan(&e.ItemID, &e.Outcome, &pid, &e.Summary, &e.Plan, &lo, &hi); err != nil {
			return nil, err
		}
		e.PolicyID, e.EstMin, e.EstMax = int64Ptr(pid), int64Ptr(lo), int64Ptr(hi)
		out = append(out, e)
	}
	return out, rows.Err()
}

// EvaluationRow is an evaluation with the item facts the summaries need.
type EvaluationRow struct {
	Evaluation
	SourceSize int64
}

// Evaluations lists every managed item's evaluation with its current size.
func (s *Store) Evaluations(ctx context.Context) ([]EvaluationRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT e.item_id, e.outcome, e.policy_id, e.summary, e.reasons, e.plan,
		e.est_min_bytes, e.est_max_bytes, COALESCE(NULLIF(p.size, 0), i.jellyfin_size, 0)
		FROM evaluations e JOIN items i ON i.id = e.item_id JOIN libraries l ON l.id = i.library_id AND l.managed = 1
		LEFT JOIN probes p ON p.item_id = e.item_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []EvaluationRow
	for rows.Next() {
		var r EvaluationRow
		var pid, lo, hi sql.NullInt64
		if err := rows.Scan(&r.ItemID, &r.Outcome, &pid, &r.Summary, &r.Reasons, &r.Plan, &lo, &hi, &r.SourceSize); err != nil {
			return nil, err
		}
		r.PolicyID, r.EstMin, r.EstMax = int64Ptr(pid), int64Ptr(lo), int64Ptr(hi)
		out = append(out, r)
	}
	return out, rows.Err()
}
