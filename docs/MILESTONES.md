# Milestones

Each milestone is a vertical slice that leaves JellyTrim working. Each entry is written so it can become a GitHub issue. Build one with `/milestone <id>`.

| Milestone | Status |
|---|---|
| M0 Project setup | Done |
| M1a Application shell | In progress |
| M1b Docker and dev stack | Not started |
| M2 Jellyfin connection and sync | Not started |
| M3 Inspection and Library | Not started |
| M4 Policies and Dry Run | Not started |
| M5 Safe encoding | Not started |
| M6 Queue, history and scheduling | Not started |
| M7 Polish and release | Not started |

---

## M0 Project setup

Agent setup (Claude Code and Codex), open-source files, design docs, ADRs.

- [x] Hooks block edits to generated files and flag em dashes; `task ai:check` passes.
- [x] `scripts/check-public.sh --all` passes on the tree and fails on a planted token.
- [x] `docs/PRODUCT.md`, `ARCHITECTURE.md`, `TRANSCODING.md`, `POLICIES.md`, `UI.md` agree with each other (spec-aligner pass).

## M1a Application shell

`cmd/jellytrim`, `internal/config`, `internal/store` with migrations, `internal/app` lifecycle, `internal/web` with layout, design tokens, navigation and empty pages, health endpoints, cross-origin protection, CI workflow.

- [ ] `jellytrim --version` prints version, commit and date.
- [ ] Starting creates `<config>/jellytrim.db` (mode 0600) and applies migrations; restarting applies nothing new.
- [ ] `/healthz` returns 200; `/readyz` returns 200 once the database is open.
- [ ] All six pages render with the shared layout; a first run redirects to `/setup`.
- [ ] A cross-origin POST is rejected with 403.
- [ ] SIGTERM shuts down cleanly within 10 seconds.
- [ ] `task check` passes; `actionlint` passes on the workflows.

Risks: Go 1.26 toolchain download; templ version pinning.

## M1b Docker and dev stack

`Dockerfile`, `docker-compose.yml` (example), `docker-compose.dev.yml`, `scripts/make-fixtures.sh`, `scripts/dev-jellyfin-bootstrap.sh`.

- [ ] The image builds for `linux/amd64` and `linux/arm64`; `docker run ... --version` works; `ffmpeg -version` in the image reports jellyfin-ffmpeg 8.
- [ ] The container runs as a non-root UID, passes its HEALTHCHECK, and stops cleanly.
- [ ] `task dev:fixtures` creates the fixture clips and probe JSON.
- [ ] `task dev:stack && task dev:bootstrap` gives a Jellyfin with Movies and TV libraries, the `dev` user, some items played and favourited, and an API key in `dev/jellyfin-api-key`. Running the bootstrap twice changes nothing.

Risks: jellyfin-ffmpeg package pinning per architecture; Jellyfin startup API differences.

## M2 Jellyfin connection and sync

`internal/jellyfin` (+ `jellyfintest`), `internal/pathmap`, `internal/library` sync, setup wizard steps 1 to 4, Settings for connection, libraries, users and path mappings.

- [ ] The wizard tests the connection and shows the server name and version, or a plain error.
- [ ] Libraries are listed with their Jellyfin locations; the user chooses which to manage.
- [ ] Users are listed; the user chooses whose watch state counts and how (any or all).
- [ ] Path mappings are suggested from library locations; the check reports "found N of M files; JellyTrim can write here".
- [ ] A sync stores every movie and episode with user data, tags, genres and collections. Counts equal Jellyfin's totals on the dev stack.
- [ ] Items removed from Jellyfin disappear after the next complete sync; a failed sync removes nothing.
- [ ] The API key never appears in any response body or log line (test).
- [ ] Fake-server tests cover 401, 503 with Retry-After, and paging.

## M3 Inspection and Library

`internal/ffmpeg` (probe), `internal/media`, probe caching, Library list with filters, item detail page, artwork proxy.

- [ ] ffprobe runs only when a file's identity changed (test with a fake runner).
- [ ] Every fixture parses to the expected model (golden tests), including HDR class, bitrate derivation, resolution class, dispositions and languages.
- [ ] Dolby Vision profile 5 and "unclear" fixtures classify as always-skip.
- [ ] The Library lists items with codec, resolution, size, HDR and watched state, with the filters from `docs/UI.md`.
- [ ] Item detail shows Jellyfin data, source details, and every audio and subtitle stream.

## M4 Policies and Dry Run

`internal/policy`, `internal/plan`, policy editor, previews, starter policies, evaluation after sync, Dry Run summary, dashboard.

- [ ] Every condition's boundaries are tested (exactly N days, unknown values, never watched).
- [ ] Precedence is deterministic; explanations are identical for identical inputs (golden test).
- [ ] The plan skips every unsafe case in `docs/TRANSCODING.md` section 3, each with its reason (table test).
- [ ] The editor reads like a sentence; the preview count equals the number of items the Dry Run would process for that policy.
- [ ] Item detail shows the winning policy, the explanation, other matches and the proposed result with an estimated size range.
- [ ] Dry Run summary and dashboard numbers match the evaluations table.
- [ ] Walkthrough on the dev stack: connect, sync, create a policy, see the Dry Run result and explanation.

This is the first usable target from the brief.

## M5 Safe encoding

`internal/encoder` (x265, QSV), hardware probe and Settings capabilities, `encoder.Sample`, `internal/pipeline` with journal, validation, backup and replace.

- [ ] Golden argument tests for x265 and QSV: each quality tier, each resolution cap, SDR, HDR10, HLG, MKV and MP4, cover art, many streams.
- [ ] The hardware probe reports x265 working locally; QSV reports "not available" cleanly without an Intel GPU.
- [ ] Safety suite: a failure injected at every step leaves the original byte-for-byte unchanged; source changed mid-encode; hard link; symlink; outside roots; EXDEV; disk full; early abort.
- [ ] Real x265 round trip on fixtures: output passes validation; every audio and subtitle stream keeps codec, language and flags; attachments and chapters kept.
- [ ] HDR10 fixture round trip keeps PQ, BT.2020, 10-bit, mastering display and content light level.
- [ ] Recovery tests for each journal state.

## M6 Queue, history and scheduling

`internal/queue`, `internal/scheduler`, Queue and History pages, Jellyfin rescan and poll, backups and Restore, "Optimise now".

- [ ] Jobs move through the statuses in `docs/ARCHITECTURE.md`; progress, speed and ETA update live.
- [ ] Pause, resume, cancel waiting jobs, retry failed jobs; concurrency setting respected.
- [ ] History shows the saving, the reason for failures, and technical details; Restore works while the backup exists.
- [ ] Scheduled sync and re-evaluation run on the interval; nothing is enqueued in Dry Run.
- [ ] `kill -9` during Encoding and during Replacing, then restart: recovery leaves a valid original or a valid new file, never neither.
- [ ] On the dev stack: a fixture is optimised, Jellyfin reports HEVC within the poll window, and its watched state is kept.

## M7 Polish and release

- [ ] Release workflow: GoReleaser binaries and checksums; images tagged `latest`, `vX.Y.Z`, `vX.Y`, `vX` for amd64 and arm64.
- [ ] `goreleaser check` and a snapshot release pass locally.
- [ ] UX review at 390, 834 and 1440 pixels; accessibility checks pass.
- [ ] README screenshots; docs complete; CHANGELOG ready for v0.1.0.

## After the MVP

NVENC, VAAPI, AV1, VideoToolbox, VMAF-guided quality, audio and subtitle rules, per-policy watch-state users, storage-pressure rules, notifications, Jellyfin webhooks, Plex and Emby adapters.
