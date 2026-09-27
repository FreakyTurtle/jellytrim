package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// PolicyRow is a stored policy. Scope, conditions and action are JSON owned
// by internal/policy.
type PolicyRow struct {
	ID         int64
	Name       string
	Enabled    bool
	Priority   int
	Scope      string
	Conditions string
	Action     string
}

// Policies lists every policy in evaluation order.
func (s *Store) Policies(ctx context.Context) ([]PolicyRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, enabled, priority, scope, conditions, action
		FROM policies ORDER BY priority, id`)
	if err != nil {
		return nil, fmt.Errorf("listing policies: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []PolicyRow
	for rows.Next() {
		var p PolicyRow
		if err := rows.Scan(&p.ID, &p.Name, &p.Enabled, &p.Priority, &p.Scope, &p.Conditions, &p.Action); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Policy loads one policy, or ErrNotFound.
func (s *Store) Policy(ctx context.Context, id int64) (PolicyRow, error) {
	var p PolicyRow
	err := s.db.QueryRowContext(ctx, `SELECT id, name, enabled, priority, scope, conditions, action FROM policies WHERE id = ?`, id).
		Scan(&p.ID, &p.Name, &p.Enabled, &p.Priority, &p.Scope, &p.Conditions, &p.Action)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

// SavePolicy inserts (ID 0) or updates a policy and returns its ID. A new
// policy goes to the end of the list unless it has a priority.
func (s *Store) SavePolicy(ctx context.Context, p PolicyRow) (int64, error) {
	now := s.unix()
	if p.ID == 0 {
		if p.Priority == 0 {
			if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(priority), 0) + 10 FROM policies`).Scan(&p.Priority); err != nil {
				return 0, err
			}
		}
		res, err := s.db.ExecContext(ctx, `INSERT INTO policies (name, enabled, priority, scope, conditions, action, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, p.Name, p.Enabled, p.Priority, p.Scope, p.Conditions, p.Action, now, now)
		if err != nil {
			return 0, fmt.Errorf("saving policy: %w", err)
		}
		return res.LastInsertId()
	}
	res, err := s.db.ExecContext(ctx, `UPDATE policies SET name = ?, enabled = ?, scope = ?, conditions = ?, action = ?, updated_at = ?
		WHERE id = ?`, p.Name, p.Enabled, p.Scope, p.Conditions, p.Action, now, p.ID)
	if err != nil {
		return 0, fmt.Errorf("saving policy: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, ErrNotFound
	}
	return p.ID, nil
}

// SetPolicyEnabled turns a policy on or off.
func (s *Store) SetPolicyEnabled(ctx context.Context, id int64, enabled bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE policies SET enabled = ?, updated_at = ? WHERE id = ?`, enabled, s.unix(), id)
	return err
}

// DeletePolicy removes a policy.
func (s *Store) DeletePolicy(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM policies WHERE id = ?`, id)
	return err
}

// MovePolicy swaps a policy with its neighbour above (up) or below, then
// renumbers priorities in steps of 10 so the order stays explicit.
func (s *Store) MovePolicy(ctx context.Context, id int64, up bool) error {
	ps, err := s.Policies(ctx)
	if err != nil {
		return err
	}
	i := -1
	for k, p := range ps {
		if p.ID == id {
			i = k
		}
	}
	if i < 0 {
		return ErrNotFound
	}
	j := i + 1
	if up {
		j = i - 1
	}
	if j < 0 || j >= len(ps) {
		return nil
	}
	ps[i], ps[j] = ps[j], ps[i]
	return s.tx(ctx, func(tx *sql.Tx) error {
		for k, p := range ps {
			if _, err := tx.ExecContext(ctx, `UPDATE policies SET priority = ? WHERE id = ?`, (k+1)*10, p.ID); err != nil {
				return err
			}
		}
		return nil
	})
}
