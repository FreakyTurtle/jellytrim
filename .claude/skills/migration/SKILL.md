---
name: migration
description: Add a SQLite schema migration to JellyTrim: next numbered file, safe on an existing database, tested, and reflected in the architecture doc.
argument-hint: "<what the migration does>"
---

Migration: **$ARGUMENTS**

1. Read `.claude/rules/store.md`.
2. Find the next number in `internal/store/migrations/` (four digits: `0007_add_x.sql`).
3. Write the migration. Forward only; JellyTrim does not run down migrations. Wrap nothing yourself: the migrator runs each file in a transaction.
   - Adding a column: `ALTER TABLE ... ADD COLUMN ...` with a default.
   - Changing a column or constraint: create the new table, copy, drop, rename (the SQLite 12-step pattern), in the same file.
   - Never delete user data (policies, settings, history) without a replacement.
4. Update the store code and its tests. Run `go test ./internal/store/...`. The migration test opens a fresh database and one at the previous version.
5. Update the data model section of `docs/ARCHITECTURE.md`.
