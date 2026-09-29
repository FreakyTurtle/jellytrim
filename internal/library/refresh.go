package library

import (
	"context"
	"errors"
	"fmt"

	"github.com/freakyturtle/jellytrim/internal/jellyfin"
	"github.com/freakyturtle/jellytrim/internal/store"
)

// RefreshItemUserData reads one item's watch state (played, play count,
// favourite, last played) from Jellyfin for every user whose watch state
// sync stores, and stores it the way sync does. The queue calls it before a
// job starts and before a file someone was watching is replaced, so the
// decision is not made on watch state from the last sync. A user who cannot
// see the item gets no row, as in sync. If nobody can see it (the item was
// removed from Jellyfin), or on any other error, nothing is stored.
func (s *Service) RefreshItemUserData(ctx context.Context, itemID string) error {
	c, err := s.Client(ctx)
	if err != nil {
		return err
	}
	st, err := s.store.Settings(ctx)
	if err != nil {
		return err
	}
	all, err := s.store.Users(ctx)
	if err != nil {
		return err
	}
	var data []store.UserData
	hidden := 0
	for _, u := range candidates(all, st) {
		it, err := c.Item(ctx, u.ID, itemID)
		if errors.Is(err, jellyfin.ErrNotFound) {
			hidden++ // this user cannot see the item
			continue
		}
		if err != nil {
			return fmt.Errorf("reading watch state for %s: %w", u.Name, err)
		}
		ud := it.UserData
		data = append(data, store.UserData{ItemID: itemID, UserID: u.ID, Played: ud.Played, PlayCount: ud.PlayCount,
			Favourite: ud.IsFavorite, LastPlayedAt: ud.LastPlayedDate})
	}
	if len(data) == 0 && hidden > 0 {
		return fmt.Errorf("reading watch state: Jellyfin no longer lists item %s: %w", itemID, jellyfin.ErrNotFound)
	}
	return s.store.ReplaceItemUserData(ctx, itemID, data)
}
