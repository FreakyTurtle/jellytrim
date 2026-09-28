// Package store keeps JellyTrim's state in a single SQLite database.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// FileName is the database file inside the config directory.
const FileName = "jellytrim.db"

// ErrNewerSchema is returned by Open when the database was written by a newer
// JellyTrim, whose schema this build cannot safely use.
var ErrNewerSchema = errors.New("the database was created by a newer version of JellyTrim")

// Store wraps the database handle. It is safe for concurrent use.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// Open opens (creating if needed) the database in dir and applies migrations.
func Open(ctx context.Context, dir string) (*Store, error) {
	path := filepath.Join(dir, FileName)
	if err := ensureFile(path); err != nil {
		return nil, err
	}
	q := url.Values{}
	// synchronous(FULL): the job journal must be on disk before the
	// filesystem step it describes happens.
	for _, p := range []string{"foreign_keys(1)", "journal_mode(WAL)", "busy_timeout(5000)", "synchronous(FULL)"} {
		q.Add("_pragma", p)
	}
	// Write transactions take the write lock when they begin. A deferred
	// transaction that reads and then writes gets SQLITE_BUSY at once when
	// another writer is active, because busy_timeout does not cover the
	// upgrade from a read lock.
	q.Set("_txlock", "immediate")
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	// SQLite allows one writer. A small pool lets reads run alongside while
	// busy_timeout serialises writers.
	db.SetMaxOpenConns(4)
	s := &Store{db: db, now: time.Now}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("opening database %s: %w", path, err)
	}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// ensureFile creates the database file with owner-only permissions, because
// it holds the Jellyfin API key.
func ensureFile(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 -- path is the configured config dir
	if err != nil {
		return fmt.Errorf("creating database file: %w", err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the handle for packages that own their own queries in tests.
func (s *Store) DB() *sql.DB { return s.db }

// Ping reports whether the database is usable.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// SetClock replaces the time source. Tests only.
func (s *Store) SetClock(now func() time.Time) { s.now = now }

func (s *Store) unix() int64 { return s.now().UTC().Unix() }

type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}
	var ms []migration
	for _, e := range entries {
		name := e.Name()
		num, _, ok := strings.Cut(name, "_")
		v, err := strconv.Atoi(num)
		if !ok || err != nil {
			return nil, fmt.Errorf("migration %s: name must start with a number and an underscore", name)
		}
		b, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return nil, err
		}
		ms = append(ms, migration{version: v, name: name, sql: string(b)})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].version < ms[j].version })
	for i := 1; i < len(ms); i++ {
		if ms[i].version == ms[i-1].version {
			return nil, fmt.Errorf("duplicate migration version %d", ms[i].version)
		}
	}
	return ms, nil
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("creating schema_migrations: %w", err)
	}
	ms, err := loadMigrations()
	if err != nil {
		return err
	}
	current, err := s.SchemaVersion(ctx)
	if err != nil {
		return err
	}
	if len(ms) == 0 {
		return errors.New("no migrations embedded")
	}
	if latest := ms[len(ms)-1].version; current > latest {
		return fmt.Errorf("%w: the database is at schema version %d but this JellyTrim only knows up to %d. "+
			"Run the newer JellyTrim again, or restore a backup of the config folder made before the upgrade",
			ErrNewerSchema, current, latest)
	}
	for _, m := range ms {
		if m.version <= current {
			continue
		}
		if err := s.apply(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) apply(ctx context.Context, m migration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return fmt.Errorf("applying migration %s: %w", m.name, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
		m.version, m.name, s.unix()); err != nil {
		return fmt.Errorf("recording migration %s: %w", m.name, err)
	}
	return tx.Commit()
}

// SchemaVersion returns the highest applied migration, or 0.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&v)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("reading schema version: %w", err)
	}
	return int(v.Int64), nil
}
