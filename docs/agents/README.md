# AI agent set-up

JellyTrim includes a set-up for AI coding agents: Claude Code and Codex. This page is a map of it for people. You do not need any of it to contribute; it is there to make agent-assisted work consistent and safe.

`.claude/` is the source of truth. The Codex files are generated from it.

## What is where

| Path | What it is |
|---|---|
| `AGENTS.md` | The shared project guide: how to work, area rules, product principles, stack, layout, commands, media-safety invariants, writing style, open-source hygiene. Codex reads it directly. |
| `CLAUDE.md` | Imports `AGENTS.md` and adds Claude Code mechanics only. |
| `.claude/agents/` | Specialist agents (one Markdown file each, with model and effort in the frontmatter). |
| `.claude/skills/<name>/SKILL.md` | Workflows, run as `/name` in Claude Code and `$name` in Codex. |
| `.claude/rules/` | Short rules for each area of the code. Claude Code loads them automatically by path. |
| `.claude/hooks/` | Shell hooks shared by Claude Code and Codex (guards, formatting, checks). |
| `.claude/output-styles/` | The optional Plain English output style. |
| `.claude/agent-memory/<agent>/MEMORY.md` | Committed memory for the agents that keep one. Public. |
| `.claude/scripts/sync-codex.py` | Generates the Codex copies of agents and skills. |
| `.claude/settings.json` | Permissions, hooks and MCP servers for Claude Code. |
| `.codex/config.toml` | MCP servers for Codex (mirrors `.mcp.json`). |
| `.codex/hooks.json` | The same hooks, wired for Codex. |
| `.codex/rules/default.rules` | Codex command rules: the same "ask first" list as Claude Code. |
| `.codex/agents/*.toml` | Generated from `.claude/agents/`. Do not edit. |
| `.agents/skills/` | Symlinks to `.claude/skills/`, so Codex finds the same skills. Do not edit. |
| `.mcp.json` | MCP servers for Claude Code: Playwright (browser checks) and Context7 (library docs). |

Personal files are gitignored: `.claude/settings.local.json`, `CLAUDE.local.md`, `.claude/agent-memory-local/`, `.claude/worktrees/` and `.codex/*.local.*`.

## Skills

| Skill | What it does |
|---|---|
| `session-start` | Reads the guide, ongoing work and milestone status, checks the repo, and reports where things stand. |
| `session-end` | Records what was done, verified and left to do in `docs/ongoing-work.md`. |
| `milestone <Mn>` | Builds a milestone from `docs/MILESTONES.md` end to end: plan, build in slices, verify, review, docs, commit. Runs in the main session because it starts other agents. |
| `feature <what>` | The same flow for a feature outside a milestone. |
| `fix <bug>` | Reproduce, find the root cause, add a failing test, fix, verify, review. |
| `verify` | Runs the CI checks locally through the verifier agent. |
| `review` | Runs the code-reviewer and conventions-keeper in parallel, plus the safety-reviewer when media handling changed, and merges the findings. |
| `verify-ui` | Checks pages in a real browser (Playwright) at phone and desktop widths. |
| `design-review` | Judges the UI against `docs/UI.md` and accessibility basics, with screenshots at three widths. |
| `docs-sync` | Brings the docs in line with the current changes, through the docs-keeper agent. |
| `check-conventions` | Checks the diff against the rules, ADRs, writing style and open-source hygiene. |
| `adr-new <decision>` | Records a design decision as a numbered ADR in `docs/adr/`. |
| `migration <what>` | Adds a SQLite schema migration, tested and documented. |
| `fixtures` | Regenerates the synthetic test media and ffprobe JSON, or adds a fixture case. |
| `dev-stack` | Brings up the throwaway Jellyfin and JellyTrim dev stack and bootstraps it. |
| `encoder-profile <encoder>` | Adds an encoder backend or retunes a quality profile, with golden tests and a hardware probe. |
| `public-check` | Checks the repository for anything that should not be published. |
| `release <version>` | Prepares and tags a release. Manual only; pushing the tag needs the maintainer's go-ahead. |
| `pr` | Opens a GitHub pull request after checks pass. Manual only. |

"Manual only" skills have `disable-model-invocation: true`: an agent never starts them on its own.

## Agents

Orchestration happens in the main session. Agents do not start other agents.

| Agent | Model / effort | Memory | Role |
|---|---|---|---|
| architect | opus / xhigh | yes | Designs a milestone or change before code is written; returns a plan in slices with tests named. Has no file-editing tools. |
| implementer | opus / high | yes | Builds one slice end to end (packages, migrations, handlers, tests) and checks it compiles, lints and passes. |
| ui-builder | opus / high | yes | Builds pages and components with templ, htmx and the CSS system, and checks them in a browser. |
| media-specialist | opus / xhigh | yes | ffmpeg, encoders, HDR, streams and containers. Designs and reviews media code. Owns `docs/TRANSCODING.md`. |
| jellyfin-specialist | sonnet / high | yes | The Jellyfin API, path mapping, library sync and the fake Jellyfin server. |
| safety-reviewer | opus / xhigh | yes | Adversarial read-only review of anything that could damage media or leak the API key. |
| code-reviewer | opus / xhigh | yes | Read-only correctness review of a diff. |
| debugger | opus / xhigh | yes | Finds and fixes the root cause of a bug, failing test or dev stack problem. |
| devops | opus / high | yes | Dockerfile, compose files, GitHub Actions, GoReleaser, Dependabot and the dev stack. |
| verifier | sonnet / low | no | Runs the CI checks and reports pass or fail. Changes nothing. |
| conventions-keeper | sonnet / medium | no | Checks a diff against rules, ADRs, writing style and hygiene. |
| docs-keeper | sonnet / medium | no | Updates docs that a change made untrue. |
| spec-aligner | sonnet / low | no | Checks a plan or docs against the product spec and ADRs. Read-only. |
| ux-reviewer | sonnet / high | no | Reviews the UI in a browser with screenshots. Cannot edit files. Loads the `design-review` skill. |
| adr-scribe | sonnet / low | no | Writes a short ADR and updates the index. |

Every agent uses opus or sonnet, so no contributor is blocked by a model they cannot use.

## Safety

The set-up has several layers. None of them replaces the real protection, which is in the code (the path boundary and validation in `internal/pipeline`) and in tests that only use `t.TempDir()` and generated fixtures.

**Hooks** (`.claude/hooks/`, used by both tools):

- `guard-media.sh` blocks `ffmpeg`, `rm`, `mv`, `cp` and `rsync` commands that touch `/Volumes`, `/mnt`, `/media` or `/srv`, where real media libraries usually live. Commands run inside the dev compose containers are allowed. This is a speed bump, not a sandbox.
- `guard-generated.sh` blocks edits to `*_templ.go`, vendored files in `internal/web/static/vendor/`, and the generated Codex copies.
- `no-em-dash.sh` flags em dashes in any file just edited.
- `format.sh` runs goimports on Go files and `templ fmt` on templ files after each edit.
- `sync-codex.sh` regenerates the Codex copies when an agent or skill changes.
- `session-context.sh` prints the branch, last commit, uncommitted changes and the latest ongoing-work entry at the start of a session.

**Permissions** (`.claude/settings.json`, mirrored in `.codex/rules/default.rules`). These commands always ask first:

- `git push`, `git reset --hard`, `git clean`, `git branch -D`
- `gh release`, `gh pr merge`
- `rm -rf`
- `docker volume rm`, `docker system prune`, `docker compose down -v`

Reading `.env` files, `dev/config/` and `*.db` files is denied.

**Open-source hygiene.** `scripts/check-public.sh` runs in the pre-commit hook and in CI, and blocks personal paths, tokens, media, databases and large files.

## Plain English output style

The Plain English output style makes Claude Code reply in plain British English based on ISO 24495-1: the point first, short sentences, active voice, no hype. It is off by default, so it is not forced on contributors.

To turn it on, either:

- run `/output-style` in Claude Code and choose **Plain English**; or
- add this to your own `.claude/settings.local.json` (gitignored):

  ```json
  {
    "outputStyle": "Plain English"
  }
  ```

Output styles do not reach subagents, so the writing rules are also in `AGENTS.md` and in each agent's prompt.

## Keeping it healthy

- **Edit only `.claude/`.** After changing an agent or skill, run `task ai:sync` (the `sync-codex.sh` hook usually does it for you). `task ai:check` fails if the Codex copies are out of date; it is part of `task check` and CI.
- **Keep `AGENTS.md` under 32 KB.** Codex truncates project guides above that size. Put detail in rules, skills or docs, and link to it.
- **Agent memory is public.** Memory files are committed to this public repository. Never record personal paths, hostnames, IP addresses, email addresses, keys, or anything about a real media library. Keep each file under 150 lines, one dated fact per bullet, and replace outdated facts rather than adding contradictions. Anything private belongs in `.claude/agent-memory-local/`, which is gitignored.
- **Rules stay short.** One file per area, a few dozen lines. If a rule needs examples, link to the doc that has them.

## Codex notes

- Codex reads `AGENTS.md` directly. It does not load `.claude/rules/` by path, so `AGENTS.md` lists which rule file to read before editing each area.
- Skills are found through the `.agents/skills/` symlinks and run as `$name`.
- Agents are generated as `.codex/agents/<name>.toml`. Each keeps the Claude agent's description, prompt and reasoning effort. Codex uses its session model, so the Claude model is recorded only as a comment. Agents with no tool that can change files (currently spec-aligner) get `sandbox_mode = "read-only"`. An agent's preloaded skills become an instruction to read that skill first.
- Hooks in `.codex/hooks.json` call the same scripts in `.claude/hooks/`, finding them through `git rev-parse --show-toplevel`, so they need a git checkout.
- `.codex/config.toml` (MCP servers) and `.codex/rules/default.rules` are loaded only after you trust the project in Codex. Check a command against the rules with `codex execpolicy check --rules .codex/rules/default.rules -- <command>`.
