@AGENTS.md

## Claude Code specifics

Put project guidance in `AGENTS.md`. This file is only for Claude Code mechanics.

- **Rules** in `.claude/rules/` load automatically when you touch files matching their `paths:`.
- **Skills** run as `/name`. Agents in `.claude/agents/` set their own model and effort; the ones with `memory: project` keep a memory file in `.claude/agent-memory/`.
- **Hooks** (`.claude/hooks/`, shared with Codex):
  - `guard-generated.sh` blocks edits to `*_templ.go`, vendored files and the Codex copies.
  - `guard-media.sh` blocks ffmpeg, rm, mv and cp against `/Volumes`, `/mnt`, `/media` and `/srv`.
  - `format.sh` runs goimports and `templ fmt` on each edited file.
  - `no-em-dash.sh` flags em dashes.
  - `sync-codex.sh` regenerates the Codex copies when an agent or skill changes.
  - `session-context.sh` prints the git state and latest progress note at session start.
- **Output style:** `.claude/output-styles/plain-english.md`. Turn it on with `/output-style` or `"outputStyle": "Plain English"` in your `.claude/settings.local.json`. Output styles do not reach subagents, so the writing rules also live in `AGENTS.md` and the agent prompts.
- **Codex copies** (`.codex/agents/*.toml`, `.agents/skills/*`) are generated. Edit `.claude/` only.
