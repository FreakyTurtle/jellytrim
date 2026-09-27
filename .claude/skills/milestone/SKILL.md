---
name: milestone
description: "Build one JellyTrim milestone from docs/MILESTONES.md end to end: plan with the architect and specialists, build in slices (parallel where files don't overlap), verify like CI, review (plus safety review for media-touching work), sync docs, then commit. Runs in the main session because it starts other agents."
argument-hint: "<milestone id, e.g. M3>"
---

You are building milestone **$ARGUMENTS** of JellyTrim. Run this in the main session: subagents cannot start other subagents, so you do the orchestration.

## 1. Load the milestone

- Read the `$ARGUMENTS` section of `docs/MILESTONES.md`: scope, acceptance criteria, risks.
- Read `docs/ongoing-work.md` for where the last session stopped.
- Check `git status` is clean and `task check` passes before you start. If it does not, fix that first.

## 2. Plan

- Run the **architect** with the milestone section and the relevant docs. For media work, also give the plan to the **media-specialist**; for Jellyfin work, to the **jellyfin-specialist**. Fold their corrections in.
- If the plan raises a real product decision (data loss risk, a change to a principle, a public interface), ask the user. Otherwise proceed.

## 3. Build, slice by slice

- For each slice, build it yourself or give it to the **implementer** (Go) or **ui-builder** (templ and CSS). Name exactly which files each agent owns.
- Independent slices run as parallel agents in one message. Slices that share files run in sequence.
- Tests first for decision logic and anything that touches files.
- After each slice: run the **verifier**. Fix failures before moving on.

## 4. Check it works

- Run the acceptance checks in the milestone section. They are not optional.
- UI work: `/verify-ui` on the affected pages.
- Anything using Jellyfin: run it against the dev stack (`/dev-stack`).

## 5. Review

- Run the **code-reviewer**. From M5 on, and for any change to `pipeline`, `queue`, `encoder`, `ffmpeg`, `plan` or file handling, also run the **safety-reviewer**, in parallel.
- Fix blockers and should-fix items. Re-run the verifier.

## 6. Docs

- Run the **docs-keeper** on the milestone diff. Tick the acceptance criteria that are met in `docs/MILESTONES.md`. Record new decisions with `/adr-new`.

## 7. Commit

- `scripts/check-public.sh` must pass (the pre-commit hook runs it).
- One commit for the milestone, or one per slice if the milestone is large: `feat(<scope>): <what>`.
- Update `docs/ongoing-work.md` with what was done, what was verified and what was not.

## 8. Report

What was built, how it was verified (commands and results), what was not verified and why, and what the next milestone starts with.
