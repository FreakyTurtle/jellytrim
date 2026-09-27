# 0006. Encode HEVC in 10-bit by default

Date: 2026-09-27
Status: Accepted

## Context
Most sources are 8-bit. Encoding HEVC in 10-bit (Main 10) gives slightly better compression and noticeably less banding in gradients, at almost no cost with modern encoders. Nearly every device that plays HEVC supports Main 10, but a few older ones do not.

## Decision
HEVC output is 10-bit by default, for all sources. HDR output is always 10-bit. An Advanced setting, "Match source bit depth", keeps 8-bit sources 8-bit for users with older players.

## Consequences
- Better quality per byte and fewer banding artefacts.
- Users with old HEVC decoders must change one setting.
