# Shared by the edit hooks. Reads the hook payload on stdin and prints the
# absolute path of every file the tool call edits, one per line.
#   Claude Code: tool_input.file_path
#   Codex:       tool_input.command holds an apply_patch body with
#                "*** Add File: p", "*** Update File: p", "*** Move to: p"
edited_paths() {
  command -v jq >/dev/null 2>&1 || return 0
  local payload cwd
  payload=$(cat)
  cwd=$(printf '%s' "$payload" | jq -r '.cwd // empty' 2>/dev/null)
  cwd=${cwd:-$PWD}
  {
    printf '%s' "$payload" | jq -r '.tool_input.file_path // empty' 2>/dev/null
    printf '%s' "$payload" | jq -r '.tool_input.command // empty' 2>/dev/null \
      | sed -nE 's/^\*\*\* (Add File|Update File|Move to): (.*)$/\2/p'
  } | while IFS= read -r p; do
    [ -z "$p" ] && continue
    case "$p" in /*) printf '%s\n' "$p" ;; *) printf '%s\n' "$cwd/$p" ;; esac
  done
}

repo_root() {
  if [ -n "${CLAUDE_PROJECT_DIR:-}" ]; then printf '%s\n' "$CLAUDE_PROJECT_DIR"
  else git rev-parse --show-toplevel 2>/dev/null || pwd; fi
}
