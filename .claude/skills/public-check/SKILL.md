---
name: public-check
description: Check the JellyTrim repository for anything that should not be published: personal paths, secrets, real media, databases, large binaries, private hostnames. Use before a commit or before the first public push.
---

1. Run `scripts/check-public.sh --all` and relay the result.
2. Run gitleaks if available: `docker run --rm -v "$PWD:/repo" zricethezav/gitleaks:latest detect --source /repo --no-banner` (scans history too).
3. Check commit authorship: `git log --format='%an <%ae>' | sort -u`. Flag any address that is not a noreply address.
4. Read `.gitignore` and `git status --ignored --short | head -50` to make sure local state (`dev/`, `.claude/settings.local.json`, databases) is ignored, not committed.
5. Skim `.claude/agent-memory/*/MEMORY.md` and `docs/ongoing-work.md` for personal details.
6. Report: clean, or each problem with the file and the fix. If history needs rewriting, say so and do not do it without the user's go-ahead.
