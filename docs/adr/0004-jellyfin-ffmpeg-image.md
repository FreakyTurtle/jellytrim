# 0004. Ship jellyfin-ffmpeg 8 in the official image

Date: 2026-09-27
Status: Accepted

## Context
JellyTrim needs ffmpeg and ffprobe with libx265, Intel QSV (oneVPL and the iHD driver), VAAPI and later NVENC, on amd64 and arm64. Distribution ffmpeg packages vary in which of these they enable. The Jellyfin project maintains jellyfin-ffmpeg, built for exactly this set of hardware and used by Jellyfin itself.

## Decision
The image is based on `debian:trixie-slim` and installs jellyfin-ffmpeg 8 from a pinned release package with a checksum per architecture. `JELLYTRIM_FFMPEG` and `JELLYTRIM_FFPROBE` point to `/usr/lib/jellyfin-ffmpeg/`. JellyTrim itself works with any ffmpeg 7 or later on the PATH when run outside the image.

## Consequences
- Users get the same hardware support Jellyfin has, including matching driver versions.
- Upgrading ffmpeg is a deliberate change to the pinned version and checksums, which Dependabot cannot do; the devops agent owns it.
- The image is larger than an Alpine image with distribution ffmpeg.
