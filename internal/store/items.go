package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Item is a movie or episode as last synced from Jellyfin.
type Item struct {
	ID             string
	LibraryID      string
	LibraryName    string
	Type           string
	Name           string
	SortName       string
	SeriesID       string
	SeriesName     string
	SeasonID       string
	SeasonName     string
	SeasonNumber   *int
	EpisodeNumber  *int
	Year           *int
	JellyfinPath   string
	LocalPath      string
	DateAdded      *time.Time
	RuntimeTicks   int64
	JellyfinSize   int64
	JellyfinCodec  string
	Tags           []string
	Genres         []string
	HasImage       bool
	SyncSkipReason string
}

// UserData is one user's watch state for one item.
type UserData struct {
	ItemID       string
	UserID       string
	Played       bool
	PlayCount    int
	Favourite    bool
	LastPlayedAt *time.Time
}

// Collection is a Jellyfin collection (box set).
type Collection struct {
	ID      string
	Name    string
	ItemIDs []string
}

// StartSync records the start of a sync and returns its ID.
func (s *Store) StartSync(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO sync_runs (started_at, status) VALUES (?, 'running')`, s.unix())
	if err != nil {
		return 0, fmt.Errorf("starting sync: %w", err)
	}
	return res.LastInsertId()
}

// FinishSync records the end of a sync. Only a complete sync removes items
// that Jellyfin no longer reports: a failed or partial sync changes nothing.
func (s *Store) FinishSync(ctx context.Context, id int64, complete bool, seen int, errText string) (removed int64, err error) {
	status := "failed"
	if complete {
		status = "complete"
	}
	err = s.tx(ctx, func(tx *sql.Tx) error {
		if complete {
			res, err := tx.ExecContext(ctx, `DELETE FROM items WHERE seen_sync_id != ?`, id)
			if err != nil {
				return fmt.Errorf("removing items no longer in Jellyfin: %w", err)
			}
			removed, _ = res.RowsAffected()
		}
		_, err := tx.ExecContext(ctx, `UPDATE sync_runs SET finished_at = ?, status = ?, items_seen = ?, error = ? WHERE id = ?`,
			s.unix(), status, seen, errText, id)
		return err
	})
	return removed, err
}

// SyncRun is one row of sync history.
type SyncRun struct {
	ID         int64
	StartedAt  time.Time
	FinishedAt *time.Time
	Status     string
	ItemsSeen  int
	Error      string
}

// LastSync returns the most recent sync attempt, or ErrNotFound.
func (s *Store) LastSync(ctx context.Context) (SyncRun, error) {
	return s.syncRun(ctx, `SELECT id, started_at, finished_at, status, items_seen, error
		FROM sync_runs ORDER BY id DESC LIMIT 1`)
}

// LastCompleteSync returns the most recent sync that finished completely,
// or ErrNotFound.
func (s *Store) LastCompleteSync(ctx context.Context) (SyncRun, error) {
	return s.syncRun(ctx, `SELECT id, started_at, finished_at, status, items_seen, error
		FROM sync_runs WHERE status = 'complete' ORDER BY id DESC LIMIT 1`)
}

func (s *Store) syncRun(ctx context.Context, q string) (SyncRun, error) {
	var r SyncRun
	var started int64
	var finished sql.NullInt64
	err := s.db.QueryRowContext(ctx, q).Scan(&r.ID, &started, &finished, &r.Status, &r.ItemsSeen, &r.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	r.StartedAt = time.Unix(started, 0).UTC()
	r.FinishedAt = timePtr(finished)
	return r, nil
}

// MarkInterruptedSyncs marks syncs left running by a crash as failed.
func (s *Store) MarkInterruptedSyncs(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sync_runs SET status = 'failed', error = 'Interrupted', finished_at = ? WHERE status = 'running'`, s.unix())
	return err
}

// UpsertItems stores a batch of items seen in sync syncID.
func (s *Store) UpsertItems(ctx context.Context, syncID int64, items []Item) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, `INSERT INTO items (id, library_id, type, name, sort_name, series_id, series_name,
			season_name, season_number, episode_number, year, jellyfin_path, local_path, date_added, runtime_ticks,
			jellyfin_size, jellyfin_codec, tags, genres, has_image, sync_skip_reason, seen_sync_id, updated_at, season_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET library_id = excluded.library_id, type = excluded.type, name = excluded.name,
			sort_name = excluded.sort_name, series_id = excluded.series_id, series_name = excluded.series_name,
			season_name = excluded.season_name, season_number = excluded.season_number, episode_number = excluded.episode_number,
			year = excluded.year, jellyfin_path = excluded.jellyfin_path, local_path = excluded.local_path,
			date_added = excluded.date_added, runtime_ticks = excluded.runtime_ticks, jellyfin_size = excluded.jellyfin_size,
			jellyfin_codec = excluded.jellyfin_codec, tags = excluded.tags, genres = excluded.genres, has_image = excluded.has_image,
			sync_skip_reason = excluded.sync_skip_reason, seen_sync_id = excluded.seen_sync_id, updated_at = excluded.updated_at,
			season_id = excluded.season_id`)
		if err != nil {
			return err
		}
		defer func() { _ = stmt.Close() }()
		now := s.unix()
		for _, it := range items {
			tags, _ := json.Marshal(nonNil(it.Tags))
			genres, _ := json.Marshal(nonNil(it.Genres))
			if _, err := stmt.ExecContext(ctx, it.ID, it.LibraryID, it.Type, it.Name, it.SortName, it.SeriesID, it.SeriesName,
				it.SeasonName, it.SeasonNumber, it.EpisodeNumber, it.Year, it.JellyfinPath, it.LocalPath, unixPtr(it.DateAdded),
				it.RuntimeTicks, it.JellyfinSize, it.JellyfinCodec, string(tags), string(genres), it.HasImage, it.SyncSkipReason,
				syncID, now, it.SeasonID); err != nil {
				return fmt.Errorf("saving item %s: %w", it.ID, err)
			}
		}
		return nil
	})
}

// ReplaceUserData replaces one user's watch state for the given items.
func (s *Store) ReplaceUserData(ctx context.Context, userID string, data []UserData) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM item_user_data WHERE user_id = ?`, userID); err != nil {
			return err
		}
		stmt, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO item_user_data (item_id, user_id, played, play_count, favorite, last_played_at)
			SELECT ?, ?, ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM items WHERE id = ?)`)
		if err != nil {
			return err
		}
		defer func() { _ = stmt.Close() }()
		for _, d := range data {
			if _, err := stmt.ExecContext(ctx, d.ItemID, userID, d.Played, d.PlayCount, d.Favourite, unixPtr(d.LastPlayedAt), d.ItemID); err != nil {
				return err
			}
		}
		return nil
	})
}

// RemoveUserDataExcept deletes watch state for users not in keep.
func (s *Store) RemoveUserDataExcept(ctx context.Context, keep []string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		ids := make([]any, len(keep))
		for i, k := range keep {
			ids[i] = k
		}
		return deleteNotIn(ctx, tx, "item_user_data", "user_id", ids)
	})
}

// ReplaceCollections stores collections and their membership. Member IDs
// may be movies, episodes, series or seasons.
func (s *Store) ReplaceCollections(ctx context.Context, cols []Collection) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM collection_items`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM collections`); err != nil {
			return err
		}
		for _, c := range cols {
			if _, err := tx.ExecContext(ctx, `INSERT INTO collections (id, name) VALUES (?, ?)`, c.ID, c.Name); err != nil {
				return err
			}
			// Members are stored as reported: a box set may hold a series
			// or season, which is not an item row. Evaluation matches an
			// episode through its series and season IDs.
			for _, id := range c.ItemIDs {
				if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO collection_items (collection_id, item_id) VALUES (?, ?)`,
					c.ID, id); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// Collections lists collections by name, without members.
func (s *Store) Collections(ctx context.Context) ([]Collection, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name FROM collections ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Collection
	for rows.Next() {
		var c Collection
		if err := rows.Scan(&c.ID, &c.Name); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

const itemColumns = `i.id, i.library_id, COALESCE(l.name, ''), i.type, i.name, i.sort_name, i.series_id, i.series_name,
	i.season_name, i.season_number, i.episode_number, i.year, i.jellyfin_path, i.local_path, i.date_added,
	i.runtime_ticks, COALESCE(i.jellyfin_size, 0), i.jellyfin_codec, i.tags, i.genres, i.has_image, i.sync_skip_reason,
	i.season_id`

type scanner interface{ Scan(...any) error }

func scanItem(r scanner, extra ...any) (Item, error) {
	var it Item
	var season, episode, year, added sql.NullInt64
	var tags, genres string
	dst := append([]any{&it.ID, &it.LibraryID, &it.LibraryName, &it.Type, &it.Name, &it.SortName, &it.SeriesID,
		&it.SeriesName, &it.SeasonName, &season, &episode, &year, &it.JellyfinPath, &it.LocalPath, &added,
		&it.RuntimeTicks, &it.JellyfinSize, &it.JellyfinCodec, &tags, &genres, &it.HasImage, &it.SyncSkipReason,
		&it.SeasonID}, extra...)
	if err := r.Scan(dst...); err != nil {
		return it, err
	}
	it.SeasonNumber = intPtr(season)
	it.EpisodeNumber = intPtr(episode)
	it.Year = intPtr(year)
	it.DateAdded = timePtr(added)
	_ = json.Unmarshal([]byte(tags), &it.Tags)
	_ = json.Unmarshal([]byte(genres), &it.Genres)
	return it, nil
}

func intPtr(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int64)
	return &v
}

// Item loads one item.
func (s *Store) Item(ctx context.Context, id string) (Item, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+itemColumns+` FROM items i LEFT JOIN libraries l ON l.id = i.library_id WHERE i.id = ?`, id)
	it, err := scanItem(row)
	if errors.Is(err, sql.ErrNoRows) {
		return it, ErrNotFound
	}
	return it, err
}

// ManagedItems lists every item in a managed library.
func (s *Store) ManagedItems(ctx context.Context) ([]Item, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+itemColumns+` FROM items i JOIN libraries l ON l.id = i.library_id
		WHERE l.managed = 1 ORDER BY i.sort_name, i.id`)
	if err != nil {
		return nil, fmt.Errorf("listing items: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// AllUserData returns the stored watch state of every enabled user, keyed
// by item ID. Sync stores it only for users who may count; callers narrow it
// to the users who count now (see internal/library).
func (s *Store) AllUserData(ctx context.Context) (map[string][]UserData, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT d.item_id, d.user_id, d.played, d.play_count, d.favorite, d.last_played_at
		FROM item_user_data d JOIN jellyfin_users u ON u.id = d.user_id WHERE u.disabled = 0`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string][]UserData{}
	for rows.Next() {
		d, err := scanUserData(rows)
		if err != nil {
			return nil, err
		}
		out[d.ItemID] = append(out[d.ItemID], d)
	}
	return out, rows.Err()
}

// ItemUserData returns one item's stored watch state for every enabled
// user, as AllUserData does.
func (s *Store) ItemUserData(ctx context.Context, itemID string) ([]UserData, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT d.item_id, d.user_id, d.played, d.play_count, d.favorite, d.last_played_at
		FROM item_user_data d JOIN jellyfin_users u ON u.id = d.user_id
		WHERE d.item_id = ? AND u.disabled = 0`, itemID)
	if err != nil {
		return nil, fmt.Errorf("reading watch state for %s: %w", itemID, err)
	}
	defer func() { _ = rows.Close() }()
	var out []UserData
	for rows.Next() {
		d, err := scanUserData(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func scanUserData(r scanner) (UserData, error) {
	var d UserData
	var last sql.NullInt64
	if err := r.Scan(&d.ItemID, &d.UserID, &d.Played, &d.PlayCount, &d.Favourite, &last); err != nil {
		return d, err
	}
	d.LastPlayedAt = timePtr(last)
	return d, nil
}

// ItemCountsByLibrary returns how many items are stored for each library.
func (s *Store) ItemCountsByLibrary(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT library_id, COUNT(*) FROM items GROUP BY library_id`)
	if err != nil {
		return nil, fmt.Errorf("counting items: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// ItemCollections returns collection IDs keyed by member ID. A member may be
// a movie, an episode, a series or a season.
func (s *Store) ItemCollections(ctx context.Context) (map[string][]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT item_id, collection_id FROM collection_items`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string][]string{}
	for rows.Next() {
		var item, col string
		if err := rows.Scan(&item, &col); err != nil {
			return nil, err
		}
		out[item] = append(out[item], col)
	}
	return out, rows.Err()
}

// SeriesNames returns series ID to name for every synced episode.
func (s *Store) SeriesNames(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT series_id, series_name FROM items WHERE series_id != '' ORDER BY series_name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}
