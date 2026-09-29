package store

import (
	"context"
	"database/sql"
	"fmt"
)

// ReplaceItemUserData replaces one item's stored watch state with data, for
// every user. Sync stores watch state only for the users whose watch state
// may count, so callers pass a row for each of those users who can see the
// item; anyone else's row for the item is removed, as the next sync would.
func (s *Store) ReplaceItemUserData(ctx context.Context, itemID string, data []UserData) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM item_user_data WHERE item_id = ?`, itemID); err != nil {
			return fmt.Errorf("clearing watch state for %s: %w", itemID, err)
		}
		for _, d := range data {
			if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO item_user_data
				(item_id, user_id, played, play_count, favorite, last_played_at)
				SELECT ?, ?, ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM items WHERE id = ?)`,
				itemID, d.UserID, d.Played, d.PlayCount, d.Favourite, unixPtr(d.LastPlayedAt), itemID); err != nil {
				return fmt.Errorf("saving watch state for %s: %w", itemID, err)
			}
		}
		return nil
	})
}
