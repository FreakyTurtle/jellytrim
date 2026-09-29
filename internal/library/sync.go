package library

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/freakyturtle/jellytrim/internal/jellyfin"
	"github.com/freakyturtle/jellytrim/internal/pathmap"
	"github.com/freakyturtle/jellytrim/internal/store"
)

const batchSize = 200

// SyncResult summarises one sync.
type SyncResult struct {
	Items    int
	Removed  int64
	Probed   int
	Failures int
}

// Run does a full cycle: sync from Jellyfin, inspect changed files, and
// evaluate every item. Only one cycle runs at a time.
func (s *Service) Run(ctx context.Context) (SyncResult, error) {
	if !s.begin() {
		return SyncResult{}, ErrBusy
	}
	res, err := s.run(ctx)
	s.end(err)
	return res, err
}

func (s *Service) run(ctx context.Context) (SyncResult, error) {
	var res SyncResult
	n, removed, err := s.sync(ctx)
	res.Items, res.Removed = n, removed
	if err != nil {
		return res, err
	}
	probed, failed, err := s.probeChanged(ctx)
	res.Probed, res.Failures = probed, failed
	if err != nil {
		return res, err
	}
	s.setPhase("Evaluating", 0, 0)
	if _, err := s.evaluate(ctx); err != nil {
		return res, err
	}
	s.log.Info("library: sync complete", "items", res.Items, "removed", res.Removed, "probed", res.Probed, "probe_failures", res.Failures)
	return res, nil
}

// sync reads every managed library from Jellyfin into the store. The attempt
// is recorded before anything else, and finished as failed on any error, so
// the scheduler sees every attempt and backs off to the interval even when
// Jellyfin is down or the setup is incomplete.
func (s *Service) sync(ctx context.Context) (seen int, removed int64, err error) {
	syncID, err := s.store.StartSync(ctx)
	if err != nil {
		return 0, 0, err
	}
	seen, err = s.syncAll(ctx, syncID)
	errText := ""
	if err != nil {
		errText = err.Error()
	}
	// Record the result even when ctx was cancelled mid-sync, so the run is
	// not left "running". Shutdown waits for this before closing the store.
	removed, ferr := s.store.FinishSync(context.WithoutCancel(ctx), syncID, err == nil, seen, errText)
	return seen, removed, errors.Join(err, ferr)
}

func (s *Service) syncAll(ctx context.Context, syncID int64) (int, error) {
	if err := s.RefreshServerInfo(ctx); err != nil {
		return 0, err
	}
	c, err := s.Client(ctx)
	if err != nil {
		return 0, err
	}
	mapper, err := s.Mapper(ctx)
	if err != nil {
		return 0, fmt.Errorf("path mappings: %w", err)
	}
	libs, users, err := s.syncScope(ctx)
	if err != nil {
		return 0, err
	}
	seen, err := s.syncItems(ctx, c, mapper, libs, syncID)
	if err != nil {
		return seen, err
	}
	if err := s.syncUserData(ctx, c, libs); err != nil {
		return seen, err
	}
	return seen, s.syncCollections(ctx, c, users)
}

// syncScope returns the managed libraries and the users whose view of the
// collections is read: the users whose watch state may count, or else the
// first enabled user.
func (s *Service) syncScope(ctx context.Context) ([]store.Library, []store.JellyfinUser, error) {
	all, err := s.store.Libraries(ctx)
	if err != nil {
		return nil, nil, err
	}
	var libs []store.Library
	for _, l := range all {
		if l.Managed {
			libs = append(libs, l)
		}
	}
	if len(libs) == 0 {
		return nil, nil, errors.New("no libraries are selected for JellyTrim to manage")
	}
	st, err := s.store.Settings(ctx)
	if err != nil {
		return nil, nil, err
	}
	users, err := s.store.Users(ctx)
	if err != nil {
		return nil, nil, err
	}
	if c := candidates(users, st); len(c) > 0 {
		return libs, c, nil
	}
	// Collections are still read through any enabled user. With nobody
	// counted, watched and favourite conditions never match and the
	// explanation says why; this user's watch state is not stored.
	for _, u := range users {
		if !u.Disabled {
			return libs, []store.JellyfinUser{u}, nil
		}
	}
	return nil, nil, errors.New("Jellyfin has no enabled users")
}

// syncItems lists every managed library without a user: the API key is an
// administrator's, so the list does not depend on one user's library access
// or parental controls. A library that comes back empty while items are
// stored for it is treated as an incomplete pass, so nothing is swept.
func (s *Service) syncItems(ctx context.Context, c *jellyfin.Client, m *pathmap.Mapper, libs []store.Library,
	syncID int64) (int, error) {
	stored, err := s.store.ItemCountsByLibrary(ctx)
	if err != nil {
		return 0, err
	}
	seen := 0
	for _, lib := range libs {
		s.setPhase("Reading "+lib.Name+" from Jellyfin", seen, 0)
		batch := make([]store.Item, 0, batchSize)
		n, err := c.AllItems(ctx, jellyfin.ItemQuery{ParentID: lib.ID}, func(it jellyfin.Item) error {
			batch = append(batch, convertItem(it, lib.ID, m))
			seen++
			if len(batch) == batchSize {
				err := s.store.UpsertItems(ctx, syncID, batch)
				batch = batch[:0]
				return err
			}
			return nil
		})
		if err != nil {
			return seen, fmt.Errorf("reading %s: %w", lib.Name, err)
		}
		if n == 0 && stored[lib.ID] > 0 {
			return seen, fmt.Errorf("Jellyfin returned no items for %s, which had %d; nothing was removed. "+
				"If the library really is empty, stop managing it in Settings", lib.Name, stored[lib.ID])
		}
		if err := s.store.UpsertItems(ctx, syncID, batch); err != nil {
			return seen, err
		}
	}
	return seen, nil
}

// convertItem maps a Jellyfin item to a store item, recording why an item
// can never be managed (a virtual item, a disc image, several versions).
func convertItem(it jellyfin.Item, libID string, m *pathmap.Mapper) store.Item {
	out := store.Item{
		ID: it.ID, LibraryID: libID, Type: it.Type, Name: it.Name, SortName: strings.ToLower(it.SortName),
		SeriesID: it.SeriesID, SeriesName: it.SeriesName, SeasonID: it.SeasonID, SeasonName: it.SeasonName,
		JellyfinPath: it.Path, RuntimeTicks: it.RunTimeTicks, Tags: it.Tags, Genres: it.Genres,
		HasImage: it.HasPrimaryImage(),
	}
	if out.SortName == "" {
		out.SortName = strings.ToLower(it.Name)
	}
	if it.Type == "Episode" {
		season, ep := it.ParentIndexNumber, it.IndexNumber
		out.SeasonNumber, out.EpisodeNumber = &season, &ep
	}
	if it.ProductionYear > 0 {
		y := it.ProductionYear
		out.Year = &y
	}
	if !it.DateCreated.IsZero() {
		d := it.DateCreated.UTC()
		out.DateAdded = &d
	}
	if len(it.MediaSources) > 0 {
		out.JellyfinSize = it.MediaSources[0].Size
		for _, st := range it.MediaSources[0].MediaStreams {
			if st.Type == "Video" {
				out.JellyfinCodec = st.Codec
				break
			}
		}
	}
	out.SyncSkipReason = skipReason(it)
	if out.SyncSkipReason == "" {
		local, err := m.ToLocal(it.Path)
		if err != nil {
			out.SyncSkipReason = "No path mapping covers " + it.Path + ". Add one in Settings."
		} else {
			out.LocalPath = local
		}
	}
	return out
}

func skipReason(it jellyfin.Item) string {
	switch {
	case it.LocationType == "Virtual":
		return "Jellyfin lists this item but has no file for it."
	case it.Path == "":
		return "Jellyfin did not report a file path."
	case it.VideoType != "" && it.VideoType != "VideoFile":
		return "Disc images and folders (" + it.VideoType + ") are not supported."
	case strings.HasSuffix(strings.ToLower(it.Path), ".strm"):
		return "Stream files (.strm) point elsewhere and cannot be optimised."
	case len(it.MediaSources) > 1:
		return "The item has several versions or parts. These are not supported yet."
	case len(it.MediaSources) == 1 && it.MediaSources[0].Protocol != "" && it.MediaSources[0].Protocol != "File":
		return "The item is not a local file."
	}
	return ""
}

// syncUserData stores watch state for every user whose watch state may
// count (see isCandidate), inactive or not, and removes it for everyone
// else. Users added in Jellyfin since the last sync are included, because
// RefreshServerInfo has just read them.
func (s *Service) syncUserData(ctx context.Context, c *jellyfin.Client, libs []store.Library) error {
	st, err := s.store.Settings(ctx)
	if err != nil {
		return err
	}
	all, err := s.store.Users(ctx)
	if err != nil {
		return err
	}
	users := candidates(all, st)
	keep := make([]string, 0, len(users))
	for i, u := range users {
		s.setPhase("Reading watch state", i, len(users))
		var data []store.UserData
		for _, lib := range libs {
			err := c.UserData(ctx, u.ID, lib.ID, func(itemID string, ud jellyfin.UserData) error {
				data = append(data, store.UserData{ItemID: itemID, Played: ud.Played, PlayCount: ud.PlayCount,
					Favourite: ud.IsFavorite, LastPlayedAt: ud.LastPlayedDate})
				return nil
			})
			if err != nil {
				return fmt.Errorf("reading watch state for %s: %w", u.Name, err)
			}
		}
		if err := s.store.ReplaceUserData(ctx, u.ID, data); err != nil {
			return err
		}
		keep = append(keep, u.ID)
	}
	return s.store.RemoveUserDataExcept(ctx, keep)
}

// syncCollections reads every collection each user can see and merges the
// memberships, so a collection one user cannot see (library access, parental
// controls) still protects its items.
func (s *Service) syncCollections(ctx context.Context, c *jellyfin.Client, users []store.JellyfinUser) error {
	s.setPhase("Reading collections", 0, 0)
	byID := map[string]*store.Collection{}
	var order []string
	for _, u := range users {
		cols, err := c.Collections(ctx, u.ID)
		if err != nil {
			return fmt.Errorf("reading collections: %w", err)
		}
		for _, col := range cols {
			ids, err := c.CollectionItemIDs(ctx, u.ID, col.ID)
			if err != nil {
				return fmt.Errorf("reading collection %s: %w", col.Name, err)
			}
			if byID[col.ID] == nil {
				byID[col.ID] = &store.Collection{ID: col.ID, Name: col.Name}
				order = append(order, col.ID)
			}
			byID[col.ID].ItemIDs = append(byID[col.ID].ItemIDs, ids...)
		}
	}
	out := make([]store.Collection, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return s.store.ReplaceCollections(ctx, out)
}
