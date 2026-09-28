# JellyTrim product specification

JellyTrim keeps each file in a Jellyfin library at the quality that makes sense now, and reduces its storage cost when that quality is no longer useful.

It uses what Jellyfin knows about each item (whether it has been watched, when, whether it is a favourite, which library it is in, how old it is) to decide when and how to shrink a file. It shrinks files with ffmpeg, safely, and explains every decision.

## Who it is for

People who run Jellyfin at home or for a small group, with a library large enough that storage matters. They know Docker and Jellyfin. They should not need to know what CRF, ICQ or an FFmpeg filter graph is.

## The problem

- 4K and high-bitrate H.264 files use a lot of disk. Most of that quality is only needed for a first viewing, if at all.
- Tools such as Tdarr, FileFlows and Unmanic can re-encode files, but they are configured through plugin chains or node graphs, and they know nothing about what has been watched.
- Getting encoding wrong destroys media: lost subtitles, lost audio tracks, HDR turned into washed-out SDR.

## What JellyTrim does

Users write **policies** that describe what they want in plain terms. JellyTrim works out how to do it safely.

Examples:
- Keep a new 4K film untouched. After it has been watched and 90 days have passed, convert it to 1080p HEVC.
- Cap a procedural TV show at 720p straight away.
- Never touch favourites.
- Convert inefficient H.264 files to HEVC at the same resolution.
- Leave files that are already efficiently encoded alone.

### Policies

A policy has three parts:

- **Scope:** what it applies to (all managed libraries, a library, a series, a season, a collection, a tag).
- **Conditions:** when it applies (watched, unwatched, favourite, not favourite, added more than N days ago, last watched more than N days ago, resolution above X, codec is or is not X, bitrate above X).
- **Action:** what to change (maximum resolution, video codec, quality, audio, subtitles), or **Protect** (never touch).

Policies read almost like English:

> **Archive watched movies.** In *Movies*, when watched, last watched more than 90 days ago, resolution above 1080p and not a favourite: convert to 1080p HEVC at High quality. Keep all audio and subtitles.

Policies are ordered. The first enabled policy that matches an item wins, and the UI shows every other policy that also matched. Every decision comes with an explanation:

> This film matches *Archive watched movies* because:
> ✓ watched
> ✓ last watched 143 days ago
> ✓ 2160p is above 1080p
> ✓ not a favourite

Before a policy is enabled, users can preview how many items it matches and what it would save. See `docs/POLICIES.md`.

### Choices users see

| Setting | Options |
|---|---|
| Quality | Maximum, High, Balanced, Space Saver |
| Resolution | Keep original, Max 2160p, Max 1080p, Max 720p, Max 480p |
| Codec | Keep existing, HEVC, AV1 (planned), H.264 |
| Audio | Preserve (default), Compress lossless audio (planned) |
| Subtitles | Preserve (default) |
| Hardware | Auto (default), or a specific encoder |

Encoder-specific settings (CRF, global quality, presets) are under **Advanced** in Settings, with the defaults explained.

### Dry Run

Dry Run is on when JellyTrim is first installed. In Dry Run, JellyTrim syncs, inspects and evaluates everything and changes nothing. It shows what it would do:

```
238 items evaluated

 91 already optimal
 87 would be converted H.264 → HEVC
 42 would be downscaled 4K → 1080p
  7 skipped (HDR or Dolby Vision)
 11 excluded by policy

Current size          4.8 TB
Estimated after     ~ 3.3 TB
Estimated saving    ~ 1.5 TB
```

Estimates are always labelled as estimates. There is a simulation for each policy too.

### Safety

JellyTrim never overwrites an original while encoding. It writes a new file next to it, checks it thoroughly, and swaps it in only if every check passes. It keeps the original as a backup for 7 days by default, with a Restore button. If JellyTrim is unsure about any file (unusual HDR, a stream the container cannot hold, a file that is hard-linked), it skips it and says why. See `docs/TRANSCODING.md`.

A file is replaced only if the saving is at least 10% (configurable). JellyTrim stops an encode early if it is clearly not going to reach that.

### Jellyfin integration

- Connects with a Jellyfin API key. Tests the connection immediately.
- Discovers libraries and lets the user choose which JellyTrim may manage.
- Reads each item's path, type, series and season, dates, runtime, streams, codecs, bitrate, HDR details, tags, genres, collections, and per-user watched state, last-played date, play count and favourite flag.
- Watched state is per user. The user chooses whose state counts: one user, or several (any of them or all of them).
- Maps paths between Jellyfin and JellyTrim (for example `/media/movies` in Jellyfin is `/mnt/media/movies` in JellyTrim). Several mappings are supported.
- Never changes Jellyfin's database or settings. After replacing a file, it asks Jellyfin to rescan that file, and checks that Jellyfin picked up the change. Because the path stays the same, watched state and favourites are kept.

### Queue and history

The queue shows each job's title, library, current and target format, the policy that chose it, progress, speed, time remaining, estimated saving and encoder. Statuses: Waiting, Analysing, Encoding, Validating, Replacing, Complete, Skipped, Failed, Cancelled and Needs attention (a job that stopped in a state a person must check; the original is safe, but it blocks new jobs for that item until resolved). Users can pause and resume the queue, cancel waiting jobs one at a time and retry failed ones. One job runs at a time by default. "Optimise now" on an item's page adds it to the front of the queue.

History lists every completed, skipped, cancelled and failed job, with the saving, a plain-English reason for any failure, and technical details (the exact ffmpeg command, its error output and the encoder). A user can restore a completed job's original while its backup exists; a restored item is then left alone until the user chooses to let JellyTrim consider it again.

### Scheduling

JellyTrim syncs with Jellyfin on an interval (every 6 hours by default) or at a daily time, and on demand. It inspects a file with ffprobe only when the file's size or modification time has changed. Encoding can be limited to chosen hours of the week: a grid of hour blocks, Monday to Sunday, each on or off, for example nights only so the GPU is free in the evening. When an hour ends, a running encode stops (the original is untouched) and starts again in the next active hour.

### Hardware

JellyTrim detects which encoders actually work on the machine by running a short test encode, not just by checking what ffmpeg lists. Settings shows the result, for example:

```
Intel UHD Graphics 770
✓ H.264 decode   ✓ HEVC decode   ✓ HEVC encode   ✗ AV1 encode
```

**Auto** picks the best working encoder for the target codec.

## Setup

First-run setup takes a few minutes:

1. Welcome.
2. Connect Jellyfin (URL and API key, tested immediately).
3. Choose libraries to manage, and whose watched state counts.
4. Map paths, with a live check that JellyTrim can see and write to the files.
5. Detect ffmpeg and hardware encoders.
6. Choose the default quality and codec.
7. Add starter policies (created disabled, except *Protect favourites*).
8. Run a Dry Run scan.

Starter policies:

- **Protect favourites:** favourites are never changed. Enabled.
- **Efficient encoding:** H.264 files, keep resolution, convert to HEVC, High quality.
- **Archive watched 4K:** watched, last watched more than 90 days ago, not a favourite, above 1080p. Convert to 1080p HEVC, High quality.
- **Space-saving television:** chosen shows or libraries. Max 720p, HEVC, Balanced.

## Principles

1. Never destroy a user's media because JellyTrim was unsure.
2. Explain every decision.
3. Policies describe intent, not FFmpeg syntax.
4. Good defaults make advanced media knowledge unnecessary.
5. Preserve streams unless the user deliberately changes them.
6. Do not transcode unnecessarily.
7. Never upscale.
8. Do not re-encode an efficient file just to satisfy a codec preference.
9. Storage saved matters, but useful quality comes first.
10. Dry Run must be genuinely useful.
11. Jellyfin awareness is the differentiator.
12. No plugin chains. No node graphs.
13. Make the common task absurdly simple.

## Not in scope

- Telemetry, accounts, cloud services or paid features.
- Built-in login. JellyTrim is meant for a private network; put it behind a reverse proxy with authentication if it needs to be reachable more widely (see `SECURITY.md`).
- Tone-mapping HDR to SDR.
- Changing Jellyfin's metadata or settings.

## MVP

1. Jellyfin connection, library sync, watched, favourite and date metadata.
2. Path mapping.
3. ffprobe inspection and the media model.
4. The policy engine with explanations and previews.
5. HEVC encoding with Intel QSV and software x265.
6. Keep resolution, or cap at 2160p, 1080p, 720p or 480p.
7. Preserve all audio and subtitles.
8. HDR10 and HLG preserved; Dolby Vision and HDR10+ skipped unless opted in.
9. Safe encode, validation, replacement, backup and restore.
10. Queue, history and scheduling.
11. Dry Run.
12. Docker image for amd64 and arm64, and automated releases.
13. A polished UI and open-source documentation.

## After the MVP

NVENC, VAAPI, AV1 (SVT-AV1 and hardware), VideoToolbox, compressing lossless audio, language and commentary rules for audio and subtitles, VMAF-guided quality selection from sample encodes, storage-pressure rules and disk-space targets, notifications and webhooks, Jellyfin webhook triggers, Plex and Emby adapters.

### Planned: quality chosen by measurement

A later version will encode a few short samples at several quality values, measure them with VMAF, estimate the full file size for each, and pick the smallest file that meets a quality threshold. The encoder layer already has a `Sample` operation for size estimates so this can be added without restructuring.
