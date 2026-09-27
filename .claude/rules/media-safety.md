---
paths:
  - "internal/ffmpeg/**"
  - "internal/encoder/**"
  - "internal/plan/**"
  - "internal/pipeline/**"
  - "internal/queue/**"
  - "internal/media/**"
---

# Media safety

JellyTrim's first principle: never destroy a user's media because JellyTrim was unsure. These rules are not negotiable. The safety-reviewer checks every change here.

## Deciding

- When in doubt, **skip with a reason**. Every skip carries a plain-English reason and the facts behind it.
- **Never upscale.** Never re-encode a file JellyTrim produced unless the new action lowers resolution further.
- **Do not transcode for nothing.** Already-efficient files (same target codec, no resolution change, bitrate within the expected range) are "already optimal".
- **Unknown is not a match.** Missing bitrate, missing dates or unknown colour data never satisfy a condition.
- **HDR**: HDR10 and HLG keep 10-bit, colour tags and static metadata. HDR10+ and Dolby Vision 7 or 8 skip unless the policy opts in. Dolby Vision 5 and unclear cases always skip. Never tone-map.
- Skip: interlaced, rotated, more than one real video stream, symlinks, hard-linked files, paths outside the configured roots, containers other than MKV and MP4, streams the container cannot hold.

## Running ffmpeg

- Only `internal/ffmpeg` calls `os/exec`. Arguments are `[]string`. Never `sh -c`. Every path is prefixed with `file:` so it cannot be read as an option or protocol.
- Always: `-nostdin -hide_banner -loglevel error -progress pipe:1 -nostats -y` and an explicit output format (`-f matroska` / `-f mp4`).
- Explicit stream maps built from ffprobe. Codec and filter options are scoped to the output stream index (`-c:v:0`, `-filter:v:0`). Everything else is `-c copy`. No stream is dropped without a recorded reason.
- Argument building is covered by golden tests. Change golden files only on purpose and review the diff.

## Replacing files

- The original is never opened for writing. The new file is written to `.<name>.jellytrim-<job>.partial` in the **same directory**.
- The replace happens only after validation passes: ffprobe comparison, stream counts, languages, dispositions, colour, duration within tolerance, decode check, minimum saving.
- Before replacing: re-check the source's device, inode, size and mtime; hard-link the original to `.<name>.jellytrim-bak-<job>`; `rename` the partial over the original; fsync the file and the directory.
- Every filesystem step is written to the journal before it happens. Recovery acts only on journal paths, never on globs.
- EXDEV is a hard failure. Never fall back to copy and delete.

## Tests

- Every failure point in the pipeline has a test that asserts the original is byte-for-byte unchanged (hash before and after).
- Tests use `t.TempDir()` and copies of fixtures. Never real media, never paths outside the temp directory.
