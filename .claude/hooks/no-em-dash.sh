#!/usr/bin/env bash
# PostToolUse hook (Claude Code and Codex): flag em dashes in any file just
# edited. JellyTrim's copy, comments and docs don't use them. Exit 2 feeds the
# lines back to the agent; the edit itself stands.
set -u
. "$(dirname "$0")/lib.sh"
found=""
EMDASH=$(printf '\342\200\224')
while IFS= read -r path; do
  [ -f "$path" ] || continue
  case "$path" in */internal/web/static/vendor/*|*/testdata/*) continue ;; esac
  grep -Iq . "$path" 2>/dev/null || continue
  hits=$(grep -n "$EMDASH" "$path" 2>/dev/null | head -5)
  [ -n "$hits" ] && found="${found}${path}:"$'\n'"${hits}"$'\n'
done < <(edited_paths)
if [ -n "$found" ]; then
  echo "Em dashes (U+2014) aren't used in JellyTrim. Replace each with a full stop, colon, comma or brackets (never a spaced hyphen):" >&2
  printf '%s' "$found" >&2
  exit 2
fi
exit 0
