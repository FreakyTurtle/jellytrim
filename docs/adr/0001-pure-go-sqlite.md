# 0001. Use SQLite through the pure-Go modernc.org/sqlite driver

Date: 2026-09-27
Status: Accepted

## Context
JellyTrim stores configuration, the item cache, policies and job history locally. It ships as one binary and a multi-architecture Docker image. The cgo SQLite driver (mattn/go-sqlite3) needs a C toolchain for every target, which complicates cross-compiling for arm64 and static builds.

## Decision
Use SQLite with `modernc.org/sqlite`, a pure-Go translation of SQLite. Build with `CGO_ENABLED=0`. Access it through `database/sql` with hand-written queries. Migrations are embedded SQL files applied by a small in-house migrator.

## Consequences
- Cross-compiling for amd64 and arm64 on any build host is trivial.
- It is somewhat slower than the C driver; JellyTrim's load (thousands of rows, few writes) is far below where that matters.
- No ORM or code generator to learn. Queries are plain SQL next to the code that uses them.
- Tests use a real SQLite file in a temp directory rather than mocks.
