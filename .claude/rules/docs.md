---
paths:
  - "docs/**"
  - "*.md"
---

# Writing docs

- Plain British English based on ISO 24495-1: lead with the point, short sentences, active voice, common words, define terms once.
- No hype, no emojis, no em dashes (use a full stop, colon, comma or brackets).
- Write for a self-hoster who knows Docker and Jellyfin but not ffmpeg internals, unless the doc is for contributors.
- Keep each doc to its purpose: `PRODUCT.md` (what and why), `ARCHITECTURE.md` (how it fits together), `TRANSCODING.md` (media decisions and encoder settings), `POLICIES.md` (policy model), `UI.md` (design system and pages), `DEVELOPMENT.md` (contributing workflow), `MILESTONES.md` (plan and status).
- Examples use placeholder values: `http://jellyfin:8096`, `your-api-key`, `/media/movies`. Never real hostnames, IPs, keys or personal paths.
- `AGENTS.md` must stay under 32 KB (Codex truncates it). Put detail in rules, skills or docs, and link.
- When a doc describes behaviour, check the code does that. Mark anything planned but not built as "(planned)".
