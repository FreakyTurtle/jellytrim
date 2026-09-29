# AGENTS.md: JellyTrim

This is the shared guide for AI coding agents working on JellyTrim. Claude Code imports it from `CLAUDE.md`; Codex reads it directly. People are welcome to read it too: it is the shortest accurate description of how the project works.

JellyTrim is a Jellyfin-aware media lifecycle optimiser. It keeps media at the quality that makes sense now, and reduces its storage cost when that quality is no longer useful: for example, converting a watched 4K film to 1080p HEVC after 90 days. It is a single Go binary with a web UI, run in Docker next to Jellyfin. It is open source (MIT) and has no telemetry.

## How to work here

- **Skills** are workflows: `/name` in Claude Code, `$name` in Codex. The main ones:
  - `/session-start`, `/session-end`: orient at the start; record progress in `docs/ongoing-work.md` at the end.
  - `/milestone <Mn>`: build a milestone from `docs/MILESTONES.md` end to end.
  - `/feature <what>`, `/fix <bug>`: build or fix outside a milestone.
  - `/verify`: run the CI checks locally. `/review`: code, conventions and safety review of the diff.
  - `/verify-ui`, `/design-review`: check pages in a browser.
  - `/dev-stack`, `/fixtures`: the throwaway Jellyfin and synthetic media for testing.
  - `/migration`, `/encoder-profile`, `/adr-new`, `/docs-sync`, `/check-conventions`, `/public-check`.
  - Manual only: `/release`, `/pr`.
- **Specialist agents** (in `.claude/agents/`; Codex copies in `.codex/agents/`): architect, implementer, ui-builder, media-specialist, jellyfin-specialist, safety-reviewer, code-reviewer, debugger, devops, verifier, conventions-keeper, docs-keeper, spec-aligner, ux-reviewer, adr-scribe. Orchestration happens in the main session; agents do not start other agents. A newly added or renamed agent becomes available as a subagent type only in a new session; a session already running does not pick it up.
- **Agent memory** is in `.claude/agent-memory/<agent>/MEMORY.md`, committed and public. Never write personal paths, hostnames, keys or anything about a real media library there.
- **"The scratchpad"** means your session's temporary directory. Screenshots, PR bodies and scratch files go there, never in the repo.
- **Commits** use conventional commits: `feat(policy): ...`, `fix(pipeline): ...`, `docs: ...`, `chore: ...`, `test: ...`, `ci: ...`. Commit only when checks pass. Never push unless asked.
- **Don't claim done until verified.** Report what you ran and what you did not. A skipped test is not a passing test.

## Area rules: read before editing

Claude Code loads these automatically by path. Codex: read the matching file before editing.

| Files | Rule |
|---|---|
| `**/*.go` | `.claude/rules/go.md` |
| `internal/store/**` | `.claude/rules/store.md` |
| `internal/web/**` | `.claude/rules/templ-ui.md` |
| `internal/{ffmpeg,encoder,plan,pipeline,queue,media}/**` | `.claude/rules/media-safety.md` |
| `internal/{jellyfin,pathmap,library}/**` | `.claude/rules/jellyfin.md` |
| `internal/policy/**` | `.claude/rules/policy.md` |
| `**/*_test.go`, `**/testdata/**` | `.claude/rules/testing.md` |
| `.github/**`, `Dockerfile`, `docker-compose*.yml`, `.goreleaser.yaml`, `Taskfile.yml`, `scripts/**` | `.claude/rules/release.md` |
| `docs/**`, `*.md` | `.claude/rules/docs.md` |

## Product principles

1. Never destroy a user's media because JellyTrim was unsure. A skipped file is always better than a damaged one.
2. Explain every decision, in plain words, in the UI.
3. Policies describe intent (quality, resolution, codec), not FFmpeg syntax.
4. Good defaults make media expertise unnecessary.
5. Preserve streams (audio, subtitles, attachments, chapters, metadata) unless the user deliberately changes them.
6. Do not transcode unnecessarily.
7. Never upscale.
8. Do not re-encode an efficient file just to satisfy a codec preference.
9. Storage saved matters, but useful quality comes first.
10. Dry Run must be genuinely useful.
11. Jellyfin awareness (watched state, favourites, dates, libraries) is the differentiator.
12. No Tdarr-style plugin chains. No FileFlows-style node graphs.
13. Make the common task absurdly simple.

The full product spec is `docs/PRODUCT.md`.

## Stack

- Go 1.26, standard library first (`net/http` ServeMux, `log/slog`, `database/sql`).
- SQLite through `modernc.org/sqlite` (pure Go, no cgo). Migrations embedded in the binary.
- templ (pinned as a Go tool: `go tool templ`), HTMX 2 (vendored), hand-written CSS, a little vanilla JS.
- ffmpeg and ffprobe at runtime (jellyfin-ffmpeg8 in the image). Encoders: x265 and Intel QSV in the MVP.
- Docker (`debian:trixie-slim`), GitHub Actions, GoReleaser, `ghcr.io/freakyturtle/jellytrim`.
- No Node, no npm, no Tailwind, no React/Vue/Svelte, no frontend build step. Ever.

## Layout

```
cmd/jellytrim/           main: flags, config, signals, version
internal/app/            wiring and lifecycle
internal/config/         bootstrap settings from env and _FILE variables
internal/store/          SQLite, embedded migrations, repositories
internal/jellyfin/       API client; jellyfintest/ fake server
internal/pathmap/        Jellyfin path <-> local path mapping (pure)
internal/media/          media model and ffprobe parsing (pure)
internal/fileid/         a file's identity for safety checks (dev, inode, size, mtime, ...)
internal/units/          formats sizes, bitrates and durations for people (pure)
internal/ffmpeg/         the only package that runs processes
internal/encoder/        encoder backends, quality maps, hardware probe
internal/policy/         policy model and evaluation with explanations (pure)
internal/plan/           decision: encode plan or skip with reasons (pure)
internal/pipeline/       safe transcode, validate, replace, journal, recovery
internal/queue/          jobs and workers
internal/library/        sync from Jellyfin, probe cache, evaluation
internal/scheduler/      periodic sync and re-evaluation
internal/timetable/      weekly processing schedule in hour blocks (pure)
internal/web/            handlers, views/ (templ), static/ (css, js, vendor)
internal/testutil/       shared test helpers (fixture loading, RequireFFmpeg)
scripts/                 fixtures, dev bootstrap, devseed, public check
docs/                    product, architecture, transcoding, policies, UI, ADRs
```

Domain packages (`media`, `pathmap`, `policy`, `plan`) are pure: no files, network, database or processes. Handlers are thin. Only `internal/ffmpeg` uses `os/exec`.

## Commands

| Command | What it does |
|---|---|
| `task setup` | Install the git pre-commit hook, download modules |
| `task generate` | `go tool templ generate` |
| `task dev` | Run locally on :8097 with templ watch (uses `./tmp/config`) |
| `task build` | Build `bin/jellytrim` |
| `task test` | `go test -race ./...` |
| `task lint` | golangci-lint |
| `task check` | Everything CI runs: generate check, vet, lint, test, ai:check, public check |
| `task dev:fixtures` | Generate synthetic media into `dev/media` and ffprobe JSON into testdata |
| `task dev:stack` | Start Jellyfin 12 and JellyTrim in Docker on the fixtures |
| `task dev:bootstrap` | Set up the dev Jellyfin (user `dev`/`dev` plus `alex`, `sam` and `robin` (password = name) with different watched and favourite states, libraries, API key in `dev/jellyfin-api-key`) |
| `go run ./scripts/devseed -config tmp/config` | Seed a config directory against the dev Jellyfin as if setup had been completed, without using the wizard (see `docs/DEVELOPMENT.md`) |
| `task ai:sync` / `task ai:check` | Regenerate / check the Codex copies of agents and skills |

## Media-safety invariants

Breaking any of these is a release blocker. Details in `.claude/rules/media-safety.md` and `docs/TRANSCODING.md`.

- The original file is never opened for writing. Output goes to a hidden `.partial` file in the same directory.
- Replacement happens only after validation passes, by hard-link backup then atomic `rename`, with fsync. Backups are kept for 7 days by default.
- Every filesystem step is journalled before it happens. Recovery acts only on journalled paths, never globs.
- Explicit stream maps; nothing dropped without a recorded reason.
- HDR10 and HLG keep their metadata; Dolby Vision 5 and unclear HDR always skip; never tone-map.
- Skip symlinks, hard-linked files, paths outside configured roots, interlaced, rotated or multi-video files.
- Arguments are `[]string`, never a shell. Paths are `file:`-prefixed.
- Dry Run is on by default. Nothing is modified in Dry Run.
- The Jellyfin API key never appears in HTML, logs, errors or diagnostics.
- Agents never run ffmpeg, rm, mv or cp against real media. Use `dev/media` or `t.TempDir()`.

## UI summary

Swiss-inspired, playful industrial minimalism. Warm off-white background, near-black text, one signal-orange accent, strong 1px and 2px borders, modular panels, big numeric metrics, compact uppercase labels, IBM Plex Sans and IBM Plex Mono, minimal radius, no shadows, controls that feel like hardware. Navigation: Dashboard, Library, Policies, Queue, History, Settings. Full system in `docs/UI.md`.

## Writing style

Everything we write (UI copy, docs, comments, commit messages, reports) is plain British English: lead with the point, short sentences, active voice, common words. No hype, no emojis, **no em dashes** (a hook flags them; use a full stop, colon, comma or brackets). The normal UI never mentions CRF, QP or FFmpeg flags.

## Open-source hygiene

This repository is public. Never commit:
- personal paths, hostnames, IP addresses, email addresses (commit authorship uses a noreply address);
- real API keys or tokens (fixtures use `deadbeefdeadbeefdeadbeefdeadbeef`);
- media files, databases, `dev/`, `.env` files, `.claude/settings.local.json`;
- files over 1 MB (except vendored fonts).

`scripts/check-public.sh` runs in the pre-commit hook and CI. Maintainers can list private strings to block in a gitignored `.public-denylist`.

## References

- `docs/PRODUCT.md`: what JellyTrim is for, users, scope, MVP boundary.
- `docs/ARCHITECTURE.md`: packages, data model, data flow, queue and pipeline states.
- `docs/TRANSCODING.md`: media model, decisions, encoders, quality maps, validation.
- `docs/POLICIES.md`: policy model, conditions, precedence, explanations.
- `docs/UI.md`: design tokens, components, pages.
- `docs/MILESTONES.md`: milestones, acceptance criteria, status.
- `docs/DEVELOPMENT.md`, `docs/TESTING.md`: how to build, run and test.
- `docs/adr/`: decisions and their reasons.
- `docs/agents/README.md`: how this agent setup is put together.
