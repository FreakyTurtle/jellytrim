package store

import "context"

// ExcludeItem stops JellyTrim changing an item until the user allows it.
func (s *Store) ExcludeItem(ctx context.Context, itemID, reason string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO exclusions (item_id, reason, created_at) VALUES (?, ?, ?)
		ON CONFLICT(item_id) DO UPDATE SET reason = excluded.reason`, itemID, reason, s.unix())
	return err
}

// IncludeItem lets JellyTrim change an excluded item again.
func (s *Store) IncludeItem(ctx context.Context, itemID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM exclusions WHERE item_id = ?`, itemID)
	return err
}

// Exclusions returns excluded item IDs with their reasons.
func (s *Store) Exclusions(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT item_id, reason FROM exclusions`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var id, reason string
		if err := rows.Scan(&id, &reason); err != nil {
			return nil, err
		}
		out[id] = reason
	}
	return out, rows.Err()
}
