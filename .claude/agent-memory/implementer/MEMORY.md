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
- 2026-09-28: Run the scale test with `-count=1`; without it `go test` may print a cached result from an earlier tree.
- 2026-09-28: SQLite's planner (no ANALYZE stats) picks an equality index plus a temp B-tree over a partial index that matches ORDER BY; lead the ordering index with the equality column instead. `SELECT DISTINCT x` also made it walk the index on x rather than a range index; dedupe in Go.
- 2026-09-28: A SQLite row just over half a 4 KB page takes a whole page, so compression only shrinks the file once rows drop below ~2 KB. `SELECT name, SUM(pgsize) FROM dbstat GROUP BY name` works with modernc.org/sqlite for a per-table breakdown.
- 2026-09-28: To measure inside another agent's test without editing it, copy it into a throwaway package under internal/ (change the package name), run, then delete the directory.
- 2026-09-28: CPU profiles on macOS over-weight syscalls (lstat, pread); confirm a hot spot with temporary wall-clock stage timers before optimising it.
- 2026-09-28: The store's pool has 4 SQLite connections, so more than 3 goroutines reading at once only queue; parallelise the CPU work (parsing, planning), not the reads.
- 2026-09-28: In the library scale test the fake Jellyfin sorts every item for every page, so sync time grows with the square of N; subtract its time by replaying the recorded requests (the test does this) before judging JellyTrim's sync.
- 2026-09-28: The scale test's "peak heap in use" is 2 to 4 times the live heap (GC slack while parsing); take a heap profile after runtime.GC() to see what is really held.
- 2026-09-28: In the library scale test at 100k items the fake Jellyfin holds ~115 MB live, and GOGC=100 lets the heap reach ~2x live, so no operation can peak much under ~240 MB there. Measure an operation's own memory against a persisted database without the fake server.
- 2026-09-28: For fast 100k experiments, build the scale database once into the scratchpad (config dir plus sparse files, ~2.5 min) with a throwaway test, then time Evaluate against it; a full scale run takes ~7.5 min because of the fake Jellyfin's sort.
- 2026-09-28: SQLite `INSERT ... SELECT ... ON CONFLICT` needs a WHERE clause on the SELECT (parsing ambiguity); `WHERE EXISTS (SELECT 1 FROM items WHERE id = ?)` doubles as a guard against FK failures.
- 2026-09-28: Paging items by `i.id > ? ORDER BY i.id LIMIT ?` needs `items i CROSS JOIN libraries l` so SQLite walks the primary key instead of sorting; a test asserts the plan has no TEMP B-TREE.
- 2026-09-28: never put throwaway Go programs under the repo's tmp/ or dev/: `go vet ./...`, `go test ./...` and lint all pick them up. Use the session scratchpad instead (outside the module), or a `_`-prefixed folder, which Go ignores.
- 2026-09-29: Go's HTTP transport silently retries a GET once when a reused keep-alive connection drops before a response, so one jellyfintest `DropConnection` fault can be absorbed and the call succeeds. For a deterministic "unreachable", inject 503s (one per client attempt) or `Close()` the fake server.
- 2026-09-29: revive `error-strings` flags `errors.New`/`fmt.Errorf` literals ending in a full stop. For user-facing sentence errors, queue uses a small `sentence` string type with an `Error()` method instead of a nolint.
- 2026-09-29: library tests run at 2027-01-01 while the fixture user "dev" was last active 2026-09-27, so with the default 90-day inactive filter dev is inactive (95 days) and counts only through the never-remove-everyone fallback. Add an active user and dev drops out; set watch_inactive_days to 0 when a test is not about the filter.
- 2026-09-29: items are not in the store before the first sync, so `mustItemID` fails there; use the fixture item ID (Bravo is 2fb4a183f1a88abb7f9defd79c21b0f2) when seeding the fake before `Run`.
- 2026-09-29: Jellyfin 12.1 creates users (wizard and POST /Users/New) with Policy.IsHidden true, and LastActivityDate stays null until the user signs in; the dev bootstrap signs each user in so the inactive filter treats them as active.
- 2026-09-29: a throwaway `_name/main.go` inside the repo can import internal packages and runs with `go run ./_name`; point it at a config dir in the scratchpad (seed one with `go run ./scripts/devseed -config <dir>`), then delete the folder.
- 2026-09-29: `st.SetSelectedUsers` only ticks users; the watch_users mode defaults to "everyone", so a test about "selected" mode must also set `store.KeyWatchUsers` to `store.WatchUsersSelected`.
- 2026-09-29: the default Jellyfin client makes 3 attempts on a 503 with 0.5 s then 1 s backoff, so a failing call in queue, library or web tests needs 3 queued `FailNext` faults and costs about 1.5 s.
