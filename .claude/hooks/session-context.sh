#!/usr/bin/env bash
# SessionStart hook (Claude Code and Codex): print a short snapshot of the
# repo. Stdout is added to the agent's context, so keep it to a few lines.
set -u
cd "${CLAUDE_PROJECT_DIR:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}" 2>/dev/null || exit 0
branch=$(git branch --show-current 2>/dev/null) || exit 0
echo "Git: on branch '${branch:-detached}'. Last commit: $(git log -1 --format='%h %s' 2>/dev/null || echo none)."
if git rev-parse --verify -q origin/main >/dev/null; then
  ab=$(git rev-list --left-right --count origin/main...HEAD 2>/dev/null) && \
    echo "Relative to origin/main (last fetch): $(echo "$ab" | awk '{print $2" ahead, "$1" behind"}')."
fi
dirty=$(git status --porcelain 2>/dev/null | wc -l | tr -d ' ')
[ "$dirty" != "0" ] && echo "Uncommitted changes: $dirty file(s)."
if [ -f docs/ongoing-work.md ]; then
  latest=$(grep -m1 '^## ' docs/ongoing-work.md | sed 's/^## //')
  [ -n "$latest" ] && echo "Latest ongoing-work entry: $latest (read docs/ongoing-work.md before starting)."
fi
exit 0
