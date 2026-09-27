#!/usr/bin/env python3
"""Generate the Codex copies of this repo's Claude Code agents and skills.

.claude/ is the source of truth. This writes:
  .codex/agents/<name>.toml   one per .claude/agents/<name>.md
  .agents/skills/<name>       a symlink to ../../.claude/skills/<name>

Usage: sync-codex.py [--check]
  --check  change nothing; exit 1 and list what is out of date.
"""

import json
import os
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
CLAUDE_AGENTS = ROOT / ".claude" / "agents"
CLAUDE_SKILLS = ROOT / ".claude" / "skills"
CODEX_AGENTS = ROOT / ".codex" / "agents"
CODEX_SKILLS = ROOT / ".agents" / "skills"

# Tools that let a Claude agent change files. An agent with a `tools` list
# containing none of these is read-only, and gets sandbox_mode = "read-only".
WRITE_TOOLS = {"Edit", "MultiEdit", "Write", "Bash", "NotebookEdit"}


def parse_frontmatter(text: str) -> tuple[dict, str]:
    """Split a Claude agent file into its frontmatter fields and body.

    Handles the subset of YAML the agent files use: `key: value` lines and
    `key:` followed by `  - item` lines.
    """
    lines = text.split("\n")
    if lines[0] != "---":
        raise ValueError("missing frontmatter")
    end = lines.index("---", 1)
    fields: dict = {}
    key = None
    for line in lines[1:end]:
        if line.startswith("  - ") and key:
            fields.setdefault(key, []).append(line[4:].strip())
            continue
        name, _, value = line.partition(":")
        key = name.strip()
        value = value.strip()
        if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
            value = value[1:-1]
        fields[key] = value if value else []
    return fields, "\n".join(lines[end + 1 :]).strip() + "\n"


def as_list(value) -> list[str]:
    if isinstance(value, list):
        return value
    return [v.strip() for v in value.split(",") if v.strip()]


def toml_string(value: str) -> str:
    return json.dumps(value, ensure_ascii=False)


def toml_multiline(value: str) -> str:
    if "'''" not in value:
        return "'''\n" + value + "'''"
    return toml_string(value)


def agent_toml(source: Path) -> str:
    fields, body = parse_frontmatter(source.read_text())
    name = fields["name"]
    notes = []
    for skill in as_list(fields.get("skills", [])):
        notes.append(
            f"Before you start, read `.claude/skills/{skill}/SKILL.md` and follow it."
        )
    if fields.get("memory") == "project":
        notes.append(
            f"Your memory file is `.claude/agent-memory/{name}/MEMORY.md`, shared with "
            "Claude Code. Read it before you start. When you learn something the next "
            "run should know, add it there and keep the file under 150 lines."
        )
    instructions = ("\n\n".join(notes) + "\n\n" if notes else "") + body

    out = [
        f"# Generated from .claude/agents/{source.name} by .claude/scripts/sync-codex.py.",
        "# Edit the source file, then run `task ai:sync`.",
    ]
    if fields.get("model"):
        out.append(f"# Claude Code model: {fields['model']}. Codex uses its session model.")
    out.append(f"name = {toml_string(name)}")
    out.append(f"description = {toml_string(fields['description'])}")
    if fields.get("effort"):
        out.append(f"model_reasoning_effort = {toml_string(fields['effort'])}")
    tools = set(as_list(fields.get("tools", [])))
    if tools and not tools & WRITE_TOOLS:
        out.append('sandbox_mode = "read-only"')
    out.append(f"developer_instructions = {toml_multiline(instructions)}")
    return "\n".join(out) + "\n"


def desired_agents() -> dict[Path, str]:
    return {
        CODEX_AGENTS / (src.stem + ".toml"): agent_toml(src)
        for src in sorted(CLAUDE_AGENTS.glob("*.md"))
    }


def desired_skills() -> dict[Path, str]:
    return {
        CODEX_SKILLS / d.name: os.path.join("..", "..", ".claude", "skills", d.name)
        for d in sorted(CLAUDE_SKILLS.iterdir())
        if (d / "SKILL.md").is_file()
    }


def plan() -> list[tuple[str, Path, str]]:
    """Return (action, path, content-or-link-target) for everything out of date."""
    actions = []
    agents = desired_agents()
    for path, content in agents.items():
        if not path.is_file() or path.read_text() != content:
            actions.append(("write", path, content))
    if CODEX_AGENTS.is_dir():
        for path in CODEX_AGENTS.glob("*.toml"):
            if path not in agents:
                actions.append(("remove", path, ""))

    skills = desired_skills()
    for path, target in skills.items():
        if not path.is_symlink() or os.readlink(path) != target:
            actions.append(("link", path, target))
    if CODEX_SKILLS.is_dir():
        for path in CODEX_SKILLS.iterdir():
            if path not in skills:
                actions.append(("remove", path, ""))
    return actions


def apply(actions: list[tuple[str, Path, str]]) -> None:
    for action, path, value in actions:
        path.parent.mkdir(parents=True, exist_ok=True)
        if action in ("remove", "link") and (path.is_symlink() or path.exists()):
            if path.is_dir() and not path.is_symlink():
                raise SystemExit(f"refusing to replace real directory {path}")
            path.unlink()
        if action == "write":
            path.write_text(value)
        elif action == "link":
            path.symlink_to(value)


def main() -> int:
    actions = plan()
    if "--check" in sys.argv[1:]:
        for action, path, _ in actions:
            print(f"out of date: {action} {path.relative_to(ROOT)}")
        if actions:
            print("Run `task ai:sync` to regenerate the Codex copies.")
        return 1 if actions else 0
    apply(actions)
    for action, path, _ in actions:
        print(f"{action} {path.relative_to(ROOT)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
