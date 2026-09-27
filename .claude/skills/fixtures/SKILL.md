---
name: fixtures
description: Regenerate JellyTrim's synthetic media fixtures (dev/media clips and internal/media/testdata ffprobe JSON) with local ffmpeg, or add a new fixture case.
argument-hint: "[new fixture case to add]"
---

Fixtures are small synthetic clips made with ffmpeg's `lavfi` sources, never real media.

- To regenerate: `task dev:fixtures`. This runs `scripts/make-fixtures.sh`, writing clips to `dev/media/` (gitignored) and ffprobe JSON to `internal/media/testdata/probe/` (committed).
- To add a case (`$ARGUMENTS`):
  1. Add it to `scripts/make-fixtures.sh`. Keep clips a few seconds long and low resolution unless resolution is the point (4K clips: 1 second).
  2. If ffmpeg cannot synthesise it (PGS subtitles, Dolby Vision, HDR10+), hand-write the ffprobe JSON in `internal/media/testdata/probe/` following the shape real ffprobe 8 output uses, and note that it is hand-written in `internal/media/testdata/README.md`.
  3. Add the expected parse result to the golden tests in `internal/media` and the expected decision to `internal/plan` tests.
- Check `scripts/check-public.sh --all` still passes: clips must never be committed.
