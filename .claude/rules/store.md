---
paths:
  - "internal/store/**"
---

# SQLite store

- Driver: `modernc.org/sqlite` (pure Go, no cgo). Open through `store.Open`, which sets WAL mode, `foreign_keys=ON`, `busy_timeout=5000` and `synchronous=NORMAL`.
- **One writer.** SQLite allows one writer at a time. The store uses a single `*sql.DB` with `SetMaxOpenConns` tuned so writes serialise; do not open a second handle.
- **Migrations** are embedded SQL files in `internal/store/migrations/NNNN_name.sql`, applied in order at startup inside a transaction each, recorded in `schema_migrations`. Forward only. Never edit a migration that has been committed; add a new one. Use `/migration`.
- **Queries** are hand-written with `database/sql` in the repository files (`items.go`, `policies.go`, `jobs.go`, ...). Use `?` placeholders only; never build SQL with string formatting from input.
- Times are stored as UTC RFC 3339 text or Unix seconds (`INTEGER`), consistently per column; document which in the schema comment.
- Booleans are `INTEGER` 0/1. JSON blobs (for example ffprobe output, policy conditions) are `TEXT` holding JSON, with a Go type that owns the shape.
- **Secrets.** The Jellyfin API key lives in the `settings` table. The database file is created with mode 0600. Never return the key from a query used to render a page.
- **Tests** use a real SQLite database in `t.TempDir()`, never a mock. They are fast; always run them.
