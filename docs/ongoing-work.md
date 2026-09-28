# Ongoing work

Newest first. Written by `/session-end`. Git history holds older entries.

## 2026-09-28: MVP built (M0 to M6), M7 mostly done

- Done: everything in M1 to M6; code and safety reviews applied; docs brought in line; UX review applied.
- Verified:
  - `task check` passes with `JELLYTRIM_REQUIRE_FFMPEG=1` (18 packages, race detector on; the only skip is the opt-in real-Jellyfin test, which also passes when enabled).
  - The Docker image builds for amd64 (has `hevc_qsv`) and arm64.
  - GoReleaser snapshot builds six archives and `checksums.txt`.
  - End to end in the Docker image against Jellyfin 12.1: sync, probe, evaluate, auto-queue, 7 real x265 replacements, each confirmed by Jellyfin. Watched and favourite flags kept; HDR10 kept (Jellyfin reports HEVC 1080p HDR10 10-bit). Unsafe files (interlaced, hard-linked) skipped; favourites protected.
- Not verified:
  - Intel QSV on real hardware (golden-argument tests only).
  - A real `kill -9` during replacement (recovery covered by unit and queue tests).
  - GitHub Actions actually running (validated with actionlint only; there is no remote yet).
  - Real 4K HDR and Dolby Vision media (synthetic fixtures only).
- Next:
  - Create the GitHub repository and push; watch the first CI run.
  - Test on an Intel machine with /dev/dri.
  - Calibrate the quality tiers with VMAF on real content.
  - Tag v0.1.0 with `/release`.
- Open questions: none blocking.

## 2026-09-27: Project set up

- Done: agent setup, design docs, ADRs, open-source files.
- Next: M1a application shell.
