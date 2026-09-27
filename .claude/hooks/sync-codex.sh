#!/usr/bin/env bash
# PostToolUse hook (Claude Code and Codex): when an agent or skill under
# .claude/ changes, regenerate the Codex copies so the two never drift.
set -u
. "$(dirname "$0")/lib.sh"
root=$(repo_root)
if edited_paths | grep -qE '/\.claude/(agents|skills)/'; then
  python3 "$root/.claude/scripts/sync-codex.py" >/dev/null 2>&1 \
    || echo "Codex copies failed to regenerate: run \`task ai:sync\`." >&2
fi
exit 0
