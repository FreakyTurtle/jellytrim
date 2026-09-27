#!/usr/bin/env bash
# PreToolUse hook on Bash (Claude Code and Codex): a speed bump that stops an
# agent running ffmpeg, rm, mv or cp against paths where a real media library
# usually lives. Agents work only on the generated fixtures under dev/ or on
# t.TempDir() copies. The real protection is the root boundary in
# internal/pipeline; this only catches obvious mistakes.
set -u
command -v jq >/dev/null 2>&1 || exit 0
cmd=$(jq -r '.tool_input.command // empty' 2>/dev/null)
[ -z "$cmd" ] && exit 0
# Commands run inside the dev compose containers see only the fixture media.
case "$cmd" in *"docker compose"*exec*|*"docker compose"*run*) exit 0 ;; esac
printf '%s' "$cmd" | grep -Eq '(^|[;&|[:space:]])(ffmpeg|rm|mv|cp|rsync)([[:space:]]|$)' || exit 0
if printf '%s' "$cmd" | grep -Eq '(^|[[:space:]"'"'"'=:])/(Volumes|mnt|media|srv)/'; then
  echo "Blocked: this command runs ffmpeg/rm/mv/cp against /Volumes, /mnt, /media or /srv, where real media libraries live. Work on the fixtures in dev/media (task dev:fixtures) or a temp copy instead. If this really is safe, ask the user to run it with the ! prefix." >&2
  exit 2
fi
exit 0
