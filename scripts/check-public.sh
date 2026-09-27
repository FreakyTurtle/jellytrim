#!/usr/bin/env bash
# Fails if content that should not be published is about to be committed:
# personal paths, credentials, media, databases, large files, or any string in
# the maintainer's gitignored .public-denylist.
#
# Usage:
#   scripts/check-public.sh          check staged changes (pre-commit hook)
#   scripts/check-public.sh --all    check every tracked and untracked, unignored file
set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"

mode=staged
[ "${1:-}" = "--all" ] && mode=all

if [ "$mode" = all ]; then
  files=$(git ls-files --cached --others --exclude-standard)
else
  files=$(git diff --cached --name-only --diff-filter=ACMR)
fi
[ -z "$files" ] && exit 0

# Content of a file as it will be committed.
content() {
  if [ "$mode" = all ]; then cat -- "$1" 2>/dev/null || true
  else git show ":$1" 2>/dev/null || true; fi
}
size_of() {
  if [ "$mode" = all ]; then wc -c <"$1" 2>/dev/null | tr -d ' '
  else git cat-file -s ":$1" 2>/dev/null || echo 0; fi
}

fail=0
report() { echo "check-public: $1" >&2; fail=1; }

# This script and the denylist mechanism mention the patterns themselves.
self_exempt='^scripts/check-public\.sh$'

max_bytes=$((1024 * 1024))
fake_token='deadbeefdeadbeefdeadbeefdeadbeef'
home_pat="${HOME:-/nonexistent-home}"
path_pat='/(Users|home)/[^/{}$ ]+/(Code|Documents|Desktop|Downloads|Library|Projects|src)/'
secret_pat='(Token="?|api[_-]?key["'"'"' :=]+|ApiKey=|X-Emby-Token[: ]+|Authorization: *Bearer +)[A-Fa-f0-9]{32}'

denylist=()
if [ -f .public-denylist ]; then
  while IFS= read -r line; do
    [ -z "$line" ] && continue
    case "$line" in \#*) continue ;; esac
    denylist+=("$line")
  done < .public-denylist
fi

while IFS= read -r f; do
  [ -z "$f" ] && continue
  if [ "$mode" = all ]; then
    # Symlinks (such as .agents/skills/*) and directories have no content to check.
    [ -L "$f" ] && continue
    [ -f "$f" ] || continue
  fi

  case "$f" in
    *.mkv|*.mp4|*.m4v|*.mov|*.avi|*.ts|*.m2ts|*.webm|*.wmv|*.flac|*.mka|*.sup|*.iso)
      report "$f: media files must not be committed (fixtures are generated)."; continue ;;
    *.db|*.db-wal|*.db-shm|*.sqlite|*.sqlite3)
      report "$f: databases must not be committed."; continue ;;
    .env|.env.*)
      [ "$f" != ".env.example" ] && { report "$f: environment files must not be committed."; continue; } ;;
    dev/*|config/*)
      report "$f: local runtime state must not be committed."; continue ;;
    .claude/settings.local.json|CLAUDE.local.md)
      report "$f: personal agent settings must not be committed."; continue ;;
  esac

  size=$(size_of "$f")
  limit=$max_bytes
  case "$f" in
    internal/web/static/vendor/fonts/*) limit=$((4 * max_bytes)) ;;
    docs/images/*) limit=$((2 * max_bytes)) ;;
  esac
  [ "${size:-0}" -gt "$limit" ] && report "$f: $size bytes is over the $limit byte limit."

  echo "$f" | grep -Eq "$self_exempt" && continue
  body=$(content "$f")
  # Skip binary files for text checks.
  printf '%s' "$body" | LC_ALL=C grep -Iq . 2>/dev/null || continue

  if [ -n "$home_pat" ] && printf '%s' "$body" | grep -Fq "$home_pat/"; then
    report "$f: contains your home directory path ($home_pat)."
  fi
  if hits=$(printf '%s' "$body" | grep -En "$path_pat" | head -3) && [ -n "$hits" ]; then
    report "$f: contains a personal-looking absolute path:"$'\n'"$hits"
  fi
  if hits=$(printf '%s' "$body" | grep -Ein "$secret_pat" | grep -v "$fake_token" | head -3) && [ -n "$hits" ]; then
    report "$f: looks like it contains a real API key or token:"$'\n'"$hits"
  fi
  for s in "${denylist[@]+"${denylist[@]}"}"; do
    if printf '%s' "$body" | grep -Fqi -- "$s"; then
      report "$f: contains a string from .public-denylist."
    fi
  done
done <<<"$files"

if [ "$fail" -ne 0 ]; then
  echo "check-public: fix the above, or ask the maintainer if a finding is a false positive." >&2
  exit 1
fi
exit 0
