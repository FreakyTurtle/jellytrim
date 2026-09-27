#!/usr/bin/env bash
# PostToolUse hook (Claude Code and Codex): format each file just written so
# gofmt and templ fmt never fail in CI. Does nothing when a formatter is not
# installed. Never blocks.
set -u
. "$(dirname "$0")/lib.sh"
root=$(repo_root)
while IFS= read -r path; do
  [ -f "$path" ] || continue
  case "$path" in
    *_templ.go) ;;
    *.go)
      if command -v goimports >/dev/null 2>&1; then
        goimports -local github.com/freakyturtle/jellytrim -w "$path" >/dev/null 2>&1
      elif command -v gofmt >/dev/null 2>&1; then gofmt -w "$path" >/dev/null 2>&1; fi ;;
    *.templ)
      (cd "$root" && go tool templ fmt "$path" >/dev/null 2>&1) ;;
  esac
done < <(edited_paths)
exit 0
