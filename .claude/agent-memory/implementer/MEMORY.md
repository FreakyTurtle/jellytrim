# implementer memory

This file is committed to a public repository. Never record personal paths, hostnames, IP addresses, email addresses, API keys or details of anyone's private infrastructure or media library.

Curate this file: keep it under 150 lines, one fact per bullet, date each entry (YYYY-MM-DD), and replace an outdated fact rather than adding a contradicting one.


- 2026-09-27: `go:embed` cannot reach a parent directory, so jellyfintest finds `internal/jellyfin/testdata` with `runtime.Caller(0)`. Read fixtures through `os.OpenRoot(dir).ReadFile(name)` to satisfy gosec G304 without a nolint.
- 2026-09-27: errcheck flags `defer resp.Body.Close()`; write `defer func() { _ = resp.Body.Close() }()`.
- 2026-09-27: revive var-naming wants `ProviderIDs` even where the JSON key is `ProviderIds`; keep the Go name idiomatic and put Jellyfin's spelling in the json tag.
- 2026-09-27: A race-enabled test binary re-executed as a fake (TestMain + env var) sleeps 1 s on a clean exit; set `GORACE=atexit_sleep_ms=0` in the child's environment.
- 2026-09-27: depguard denies os/exec everywhere except internal/ffmpeg and _test.go files, so internal/testutil looks tools up on PATH by hand instead of exec.LookPath.
- 2026-09-27: misspell (UK) rejects "hardlinked"; write "hard-linked". gosec G703 flags os.Stat on a path from an environment variable.
- 2026-09-27: A concurrency test for SQLite locking only catches missing `_txlock=immediate` if many goroutines run read-then-write transactions on different keys at once; a single contended key passes either way once a unique index exists.
- 2026-09-27: Migrations run inside a transaction, so `PRAGMA foreign_keys=OFF` is a no-op there; recreating a table that nothing references (create new, copy, drop, rename) is still safe with foreign keys on.
- 2026-09-27: jellyfintest collection members must exist as fake items to be listed, so a box set holding a series needs `AddItem` of a `Series` (or `Season`) item with a path under the library.
