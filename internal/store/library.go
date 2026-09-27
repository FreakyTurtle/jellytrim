package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// Library is a Jellyfin library and whether JellyTrim manages it.
type Library struct {
	ID             string
	Name           string
	CollectionType string
	Locations      []string
	Managed        bool
}

// JellyfinUser is a Jellyfin user and whether their watch state counts.
type JellyfinUser struct {
	ID       string
	Name     string
	Disabled bool
	Selected bool
}

// PathMapping pairs a Jellyfin prefix with a local prefix.
type PathMapping struct {
	ID             int64
	JellyfinPrefix string
	LocalPrefix    string
}

// ReplaceLibraries stores the libraries Jellyfin reports, keeping each
// library's managed flag and removing libraries that no longer exist.
func (s *Store) ReplaceLibraries(ctx context.Context, libs []Library) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		ids := make([]any, 0, len(libs))
		for _, l := range libs {
			locs, _ := json.Marshal(nonNil(l.Locations))
			if _, err := tx.ExecContext(ctx, `INSERT INTO libraries (id, name, collection_type, locations, managed, updated_at)
				VALUES (?, ?, ?, ?, 0, ?)
				ON CONFLICT(id) DO UPDATE SET name = excluded.name, collection_type = excluded.collection_type,
				locations = excluded.locations, updated_at = excluded.updated_at`,
				l.ID, l.Name, l.CollectionType, string(locs), s.unix()); err != nil {
				return fmt.Errorf("saving library %s: %w", l.Name, err)
			}
			ids = append(ids, l.ID)
		}
		return deleteNotIn(ctx, tx, "libraries", "id", ids)
	})
}

// Libraries lists every known library, by name.
func (s *Store) Libraries(ctx context.Context) ([]Library, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, collection_type, locations, managed FROM libraries ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("listing libraries: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Library
	for rows.Next() {
		var l Library
		var locs string
		if err := rows.Scan(&l.ID, &l.Name, &l.CollectionType, &locs, &l.Managed); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(locs), &l.Locations)
		out = append(out, l)
	}
	return out, rows.Err()
}

// SetManagedLibraries marks exactly the given libraries as managed.
func (s *Store) SetManagedLibraries(ctx context.Context, ids []string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE libraries SET managed = 0`); err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, `UPDATE libraries SET managed = 1 WHERE id = ?`, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// ReplaceUsers stores the Jellyfin users, keeping each user's selected flag.
func (s *Store) ReplaceUsers(ctx context.Context, users []JellyfinUser) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		ids := make([]any, 0, len(users))
		for _, u := range users {
			if _, err := tx.ExecContext(ctx, `INSERT INTO jellyfin_users (id, name, disabled, selected, updated_at)
				VALUES (?, ?, ?, 0, ?)
				ON CONFLICT(id) DO UPDATE SET name = excluded.name, disabled = excluded.disabled, updated_at = excluded.updated_at`,
				u.ID, u.Name, u.Disabled, s.unix()); err != nil {
				return fmt.Errorf("saving user: %w", err)
			}
			ids = append(ids, u.ID)
		}
		return deleteNotIn(ctx, tx, "jellyfin_users", "id", ids)
	})
}

// Users lists Jellyfin users by name.
func (s *Store) Users(ctx context.Context) ([]JellyfinUser, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, disabled, selected FROM jellyfin_users ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, fmt.Errorf("listing users: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []JellyfinUser
	for rows.Next() {
		var u JellyfinUser
		if err := rows.Scan(&u.ID, &u.Name, &u.Disabled, &u.Selected); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// SelectedUsers lists users whose watch state counts, excluding disabled ones.
func (s *Store) SelectedUsers(ctx context.Context) ([]JellyfinUser, error) {
	all, err := s.Users(ctx)
	if err != nil {
		return nil, err
	}
	var out []JellyfinUser
	for _, u := range all {
		if u.Selected && !u.Disabled {
			out = append(out, u)
		}
	}
	return out, nil
}

// SetSelectedUsers marks exactly the given users as counting for watch state.
func (s *Store) SetSelectedUsers(ctx context.Context, ids []string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE jellyfin_users SET selected = 0`); err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, `UPDATE jellyfin_users SET selected = 1 WHERE id = ?`, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// PathMappings lists mappings in the order the user entered them.
func (s *Store) PathMappings(ctx context.Context) ([]PathMapping, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, jellyfin_prefix, local_prefix FROM path_mappings ORDER BY position, id`)
	if err != nil {
		return nil, fmt.Errorf("listing path mappings: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []PathMapping
	for rows.Next() {
		var m PathMapping
		if err := rows.Scan(&m.ID, &m.JellyfinPrefix, &m.LocalPrefix); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ReplacePathMappings stores a complete new set of mappings.
func (s *Store) ReplacePathMappings(ctx context.Context, ms []PathMapping) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM path_mappings`); err != nil {
			return err
		}
		for i, m := range ms {
			if _, err := tx.ExecContext(ctx, `INSERT INTO path_mappings (jellyfin_prefix, local_prefix, position) VALUES (?, ?, ?)`,
				m.JellyfinPrefix, m.LocalPrefix, i); err != nil {
				return err
			}
		}
		return nil
	})
}

// tx runs fn in a transaction.
func (s *Store) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// deleteNotIn removes rows whose key is not in keep. The table and column
// names are constants from this package, never user input.
func deleteNotIn(ctx context.Context, tx *sql.Tx, table, col string, keep []any) error {
	if len(keep) == 0 {
		_, err := tx.ExecContext(ctx, "DELETE FROM "+table) // #nosec G202 -- constant table name
		return err
	}
	q := "DELETE FROM " + table + " WHERE " + col + " NOT IN (?" + repeat(",?", len(keep)-1) + ")" // #nosec G202 -- constant names
	_, err := tx.ExecContext(ctx, q, keep...)
	return err
}

func repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func unixPtr(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return t.UTC().Unix()
}

func timePtr(n sql.NullInt64) *time.Time {
	if !n.Valid {
		return nil
	}
	t := time.Unix(n.Int64, 0).UTC()
	return &t
}
