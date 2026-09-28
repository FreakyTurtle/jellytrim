package store

import (
	"context"
	"database/sql"
	"time"
)

// CapabilityRow is one encoder backend's stored test result. Detail is JSON
// owned by internal/encoder.
type CapabilityRow struct {
	Backend   string
	Available bool
	Detail    string
	TestedAt  time.Time
}

// SaveCapabilities replaces the stored hardware test results.
func (s *Store) SaveCapabilities(ctx context.Context, rows []CapabilityRow) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM capabilities`); err != nil {
			return err
		}
		for _, r := range rows {
			if _, err := tx.ExecContext(ctx, `INSERT INTO capabilities (backend, available, detail, tested_at) VALUES (?, ?, ?, ?)`,
				r.Backend, r.Available, r.Detail, r.TestedAt.UTC().Unix()); err != nil {
				return err
			}
		}
		return nil
	})
}

// Capabilities returns the stored hardware test results.
func (s *Store) Capabilities(ctx context.Context) ([]CapabilityRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT backend, available, detail, tested_at FROM capabilities ORDER BY backend`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []CapabilityRow
	for rows.Next() {
		var r CapabilityRow
		var at int64
		if err := rows.Scan(&r.Backend, &r.Available, &r.Detail, &at); err != nil {
			return nil, err
		}
		r.TestedAt = time.Unix(at, 0).UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}
