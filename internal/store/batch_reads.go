package store

import (
	"context"
	"fmt"
)

// Evaluating a large library walks it a page of items at a time
// (ManagedItemsAfter) and reads each page's watch state and probes by item
// ID, so it never holds the whole library in memory.

// maxIDsPerQuery bounds the item IDs bound to one IN list: well under
// SQLite's limit on host parameters, and small enough that each query is
// short.
const maxIDsPerQuery = 500

// inChunks calls fn for consecutive chunks of ids of at most maxIDsPerQuery,
// with the chunk as query arguments and a matching "?,?,..." list.
func inChunks(ids []string, fn func(marks string, args []any) error) error {
	for start := 0; start < len(ids); start += maxIDsPerQuery {
		chunk := ids[start:min(start+maxIDsPerQuery, len(ids))]
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		if err := fn("?"+repeat(",?", len(chunk)-1), args); err != nil {
			return err
		}
	}
	return nil
}

// managedItemsAfterQuery is one page of managed items. CROSS JOIN keeps
// items as the outer loop, so SQLite reads them in primary key order and
// stops at the limit instead of sorting them all.
const managedItemsAfterQuery = `SELECT ` + itemColumns + ` FROM items i CROSS JOIN libraries l
	WHERE l.id = i.library_id AND l.managed = 1 AND i.id > ? ORDER BY i.id LIMIT ?`

// ManagedItemsAfter returns up to limit items in managed libraries whose ID
// sorts after afterID, in ID order. Start with "" and pass the last ID of
// each page to get the next; an empty page is the end. Paging by ID walks
// the primary key, so every page is as quick as the first.
func (s *Store) ManagedItemsAfter(ctx context.Context, afterID string, limit int) ([]Item, error) {
	rows, err := s.db.QueryContext(ctx, managedItemsAfterQuery, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("listing items: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]Item, 0, limit)
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, fmt.Errorf("listing items: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ItemsByIDs returns the items with the given IDs, keyed by ID. IDs with no
// item are left out.
func (s *Store) ItemsByIDs(ctx context.Context, ids []string) (map[string]Item, error) {
	out := make(map[string]Item, len(ids))
	err := inChunks(ids, func(marks string, args []any) error {
		rows, err := s.db.QueryContext(ctx, `SELECT `+itemColumns+` FROM items i LEFT JOIN libraries l ON l.id = i.library_id
			WHERE i.id IN (`+marks+`)`, args...) // #nosec G202 -- only placeholders are added
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			it, err := scanItem(rows)
			if err != nil {
				return err
			}
			out[it.ID] = it
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("reading items: %w", err)
	}
	return out, nil
}

// UserDataFor returns the selected users' watch state for the given items,
// keyed by item ID, as AllUserData does for every item.
func (s *Store) UserDataFor(ctx context.Context, ids []string) (map[string][]UserData, error) {
	out := make(map[string][]UserData, len(ids))
	err := inChunks(ids, func(marks string, args []any) error {
		rows, err := s.db.QueryContext(ctx, `SELECT d.item_id, d.user_id, d.played, d.play_count, d.favorite, d.last_played_at
			FROM item_user_data d JOIN jellyfin_users u ON u.id = d.user_id
			WHERE d.item_id IN (`+marks+`) AND u.selected = 1 AND u.disabled = 0`, args...) // #nosec G202 -- only placeholders are added
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			d, err := scanUserData(rows)
			if err != nil {
				return err
			}
			out[d.ItemID] = append(out[d.ItemID], d)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("reading watch state: %w", err)
	}
	return out, nil
}

// ProbeSummariesFor returns the probe summaries of the given items, keyed
// by item ID, without reading the JSON. Items with no probe are left out.
func (s *Store) ProbeSummariesFor(ctx context.Context, ids []string) (map[string]ProbeSummary, error) {
	out := make(map[string]ProbeSummary, len(ids))
	err := inChunks(ids, func(marks string, args []any) error {
		rows, err := s.db.QueryContext(ctx, `SELECT `+probeSummaryColumns+` FROM probes WHERE item_id IN (`+marks+`)`,
			args...) // #nosec G202 -- only placeholders are added
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			p, err := scanProbeSummary(rows)
			if err != nil {
				return err
			}
			out[p.ItemID] = p
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("reading probe summaries: %w", err)
	}
	return out, nil
}

// ProbesByIDs returns the cached probes of the given items with their
// ffprobe output, keyed by item ID, reading up to maxIDsPerQuery in each
// query. Items with no probe are left out.
func (s *Store) ProbesByIDs(ctx context.Context, ids []string) (map[string]Probe, error) {
	out := make(map[string]Probe, len(ids))
	err := inChunks(ids, func(marks string, args []any) error {
		rows, err := s.db.QueryContext(ctx, `SELECT `+probeColumns+` FROM probes WHERE item_id IN (`+marks+`)`,
			args...) // #nosec G202 -- only placeholders are added
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			p, err := scanProbe(rows)
			if err != nil {
				return err
			}
			out[p.ItemID] = p
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("reading probes: %w", err)
	}
	return out, nil
}
