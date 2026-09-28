# code-reviewer memory

This file is committed to a public repository. Never record personal paths, hostnames, IP addresses, email addresses, API keys or details of anyone's private infrastructure or media library.

Curate this file: keep it under 150 lines, one fact per bullet, date each entry (YYYY-MM-DD), and replace an outdated fact rather than adding a contradicting one.


## Recurring mistake classes

- 2026-09-27: Deferred transactions that read then write (check-then-insert) get SQLITE_BUSY immediately under WAL, because busy_timeout does not cover a read-to-write upgrade. Look for SELECT-then-INSERT inside `s.tx`; prefer `_txlock=immediate` or one conditional statement plus a partial unique index.
- 2026-09-27: Goroutines started in `app.Run` (hardware test, scheduler) are not joined before `store.Close`; check every `go` in wiring code has a matching wait in `shutdown`, and that HTTP stops taking work first.
- 2026-09-27: `library.evaluate`, `Preview` and `Reassess` each re-implement the per-item gates (exclusions, sync skip, pending probe). Diff them against each other on every change; a gate added to one is usually missing from the others.
- 2026-09-27: Safety-relevant lookups use `if v, err := ...; err == nil` and fail open (exclusions, optimised loop guard). Flag any ignored error whose default value means "go ahead".
- 2026-09-27: Protective conditions (favourite, collection) are easy to under-match: series-level collection membership not applied to episodes, "all users" mode applied to favourites. Ask "does this make a Protect policy match less?" for every membership or user-state change.
- 2026-09-27: Failures before `StartSync` leave no sync_runs row, so anything keyed on the last sync (scheduler due check) retries every tick. Check that every attempt is recorded or backed off.
- 2026-09-27: Whole-library loads (`Probes()` with JSON, `loadSnapshot`) get reused for single-item or identity-only needs (per job, per preview keystroke). Check call frequency before accepting a full snapshot.
- 2026-09-27: Jellyfin replace-all helpers (`ReplaceLibraries`, `ReplaceUsers`) delete rows outside the mark-and-sweep guarantee, and FK cascades then remove items, probes and evaluations. Treat an empty or shrunken list as suspect.
- 2026-09-27: gofmt drift on aligned struct field comments after hand edits; run `golangci-lint run` on touched packages before reporting ready.
