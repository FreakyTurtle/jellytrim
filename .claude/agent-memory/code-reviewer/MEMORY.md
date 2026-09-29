# code-reviewer memory

This file is committed to a public repository. Never record personal paths, hostnames, IP addresses, email addresses, API keys or details of anyone's private infrastructure or media library.

Curate this file: keep it under 150 lines, one fact per bullet, date each entry (YYYY-MM-DD), and replace an outdated fact rather than adding a contradicting one.


## Recurring mistake classes

- 2026-09-27: Deferred transactions that read then write (check-then-insert) get SQLITE_BUSY immediately under WAL, because busy_timeout does not cover a read-to-write upgrade. Look for SELECT-then-INSERT inside `s.tx`; prefer `_txlock=immediate` or one conditional statement plus a partial unique index.
- 2026-09-27: Goroutines started in `app.Run` (hardware test, scheduler) are not joined before `store.Close`; check every `go` in wiring code has a matching wait in `shutdown`, and that HTTP stops taking work first.
- 2026-09-27: `library.evaluate`, `Preview` and `Reassess` each re-implement the per-item gates (exclusions, sync skip, pending probe). Diff them against each other on every change; a gate added to one is usually missing from the others.
- 2026-09-27: Safety-relevant lookups use `if v, err := ...; err == nil` and fail open (exclusions, optimised loop guard). Flag any ignored error whose default value means "go ahead".
- 2026-09-29: Protective conditions (favourite, collection) are easy to under-match: series-level collection membership not applied to episodes, "all users" mode applied to favourites, a user filter meant for the watched share (inactive accounts) also dropping those users' favourites. Ask "does this make a Protect policy match less?" for every membership, user-state or who-counts change.
- 2026-09-29: A new setting's default in `settingDefaults` applies to upgraded installs unless the migration writes a value. Check ADR or changelog claims that "existing installs keep their behaviour" against every new default.
- 2026-09-29: Anything that makes a running job wait (playback wait before replace, up to hours) opens a window where Dry Run, policies and watch state change, but the pipeline re-checks only file identity. Ask what is re-checked after the wait.
- 2026-09-29: Named presets stored as a raw number (majority = 51%) drift from their meaning at large counts (ceil(0.51 x 51) = 27, not 26). Test presets at large N, not only 1 to 5.
- 2026-09-27: Failures before `StartSync` leave no sync_runs row, so anything keyed on the last sync (scheduler due check) retries every tick. Check that every attempt is recorded or backed off.
- 2026-09-27: Whole-library loads (`Probes()` with JSON, `loadSnapshot`) get reused for single-item or identity-only needs (per job, per preview keystroke). Check call frequency before accepting a full snapshot.
- 2026-09-27: Jellyfin replace-all helpers (`ReplaceLibraries`, `ReplaceUsers`) delete rows outside the mark-and-sweep guarantee, and FK cascades then remove items, probes and evaluations. Treat an empty or shrunken list as suspect.
- 2026-09-27: gofmt drift on aligned struct field comments after hand edits; run `golangci-lint run` on touched packages before reporting ready.
