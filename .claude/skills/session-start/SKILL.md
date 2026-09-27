---
name: session-start
description: Orient at the start of a JellyTrim session. Reads the guide, ongoing work and milestone status, checks the repo state, and reports where things stand and what to do next.
---

1. Read `AGENTS.md` if it is not already in context.
2. Read the top entry of `docs/ongoing-work.md`.
3. Read the status table at the top of `docs/MILESTONES.md`.
4. Run `git status --short`, `git log --oneline -5`.
5. Report in five lines or fewer: current milestone, last thing done, anything unverified or broken, uncommitted work, and the suggested next step.
