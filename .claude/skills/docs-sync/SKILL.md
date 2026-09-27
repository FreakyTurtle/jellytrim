---
name: docs-sync
description: Bring JellyTrim's docs in line with the current changes using the docs-keeper agent.
argument-hint: "[diff range]"
---

Run the **docs-keeper** agent on `$ARGUMENTS` (default: `git diff HEAD`, or the last commit if clean). Relay the list of files it changed. Check that `AGENTS.md` is still under 32 KB (`wc -c AGENTS.md`).
