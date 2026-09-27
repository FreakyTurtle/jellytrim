# Transcoding

This document describes how JellyTrim inspects a file, decides what to do with it, encodes it, checks the result and replaces the original. The media-specialist agent owns it. The code must match it.

The rule behind every section: **when JellyTrim is not sure a change is safe, it skips the file and says why.**

## 1. Inspection

JellyTrim runs ffprobe twice per file, and only when the file's device, inode, size or modification time has changed since the last probe.

1. Streams, format and chapters:
   `ffprobe -v error -show_format -show_streams -show_chapters -of json file:<path>`
2. The first video frame's side data (HDR metadata that is often only in frames). This probes `v:0`; if the main video is not the first video stream, the frame data is missing and PQ content is treated as Unclear:
   `ffprobe -v error -select_streams v:0 -read_intervals %+#1 -show_frames -show_entries frame=color_transfer,color_primaries,color_space,side_data_list -of json file:<path>`

The raw JSON is stored, so parsing improvements apply without probing again.

## 2. Media model (`internal/media`)

| Type | Fields |
|---|---|
| `File` | path, size, duration, container (format name), overall bitrate, tags, chapters count, streams |
| `VideoStream` | index, codec, profile, width, height, sample and display aspect ratio, frame rate, field order, bit depth, pixel format, colour primaries, transfer, matrix, range, bitrate (derived), rotation, `attached_pic`, Dolby Vision config, HDR metadata |
| `AudioStream` | index, codec, profile (for DTS-HD MA, TrueHD Atmos), channels, layout, bitrate, language, title, disposition |
| `SubtitleStream` | index, codec, language, title, disposition (default, forced, hearing impaired), text or bitmap |
| `Attachment` | index, codec or MIME type (fonts for ASS), file name |
| `DataStream` | index, codec tag (for example `tmcd`) |
| `Disposition` | default, forced, hearing impaired, visual impaired, commentary, original, attached picture |

### Derived facts

- **Main video stream:** the first video stream that is not an `attached_pic`. If there is more than one non-`attached_pic` video stream, the file is skipped.
- **Video bitrate:** the stream's `bit_rate`; else its `BPS` tag (MKV); else the format bitrate minus the audio bitrates; else size divided by duration minus audio. Lossy audio with no stated bitrate (ffmpeg's MKV muxer writes none for AAC or Opus) counts as 96 kbps per channel, capped at 768 kbps: a deliberately high guess, so the video estimate errs low, which can only make a file look already efficient. Lossless or unknown audio without a bitrate makes the video bitrate unknown, and bitrate conditions then do not match. The UI says where the number came from ("from the stream", "estimated from the container bitrate").
- **Resolution class:** by width *or* height, so letterboxed films count by their width.

  | Class | Width at least | or height at least |
  |---|---|---|
  | 2160p | 3200 | 1800 |
  | 1440p | 2240 | 1260 |
  | 1080p | 1600 | 900 |
  | 720p | 1120 | 630 |
  | 576p | 900 | 500 |
  | 480p | below the above | |

- **Language** comes only from the stream's `language` tag. `und`, empty and missing are "unknown". Stream order never implies language.
- **Commentary** is the `comment` disposition, or a title containing "commentary" (case-insensitive).

### HDR classification

| Class | Detected by | Default |
|---|---|---|
| SDR | Transfer is BT.709, BT.601 (`smpte170m`), BT.470 (`gamma22`, `gamma28`) or sRGB; or transfer and primaries both unset with no HDR evidence at all (no mastering or light-level metadata, no Dolby Vision or HDR10+, no BT.2020 matrix), whatever the bit depth; the output then keeps the same unset tags | Transcode |
| HDR10 | Transfer `smpte2084` (PQ), primaries `bt2020`, no Dolby Vision, no HDR10+ | Transcode, keeping 10-bit, colour tags and static metadata |
| HLG | Transfer `arib-std-b67` | Transcode, keeping 10-bit and colour tags |
| HDR10+ | `HDR Dynamic Metadata SMPTE2094-40` in frame side data | Skip unless the policy allows reducing to HDR10 |
| Dolby Vision with HDR10 base | `DOVI configuration record` with profile 7, or profile 8 with compatibility ID 1, with the base-layer flag set and PQ + BT.2020 on the stream | Skip unless the policy allows reducing to HDR10 |
| Dolby Vision without a usable base | Profile 5, profile 8 with compatibility ID 0, 2 or 4, or any other profile | Always skip |
| Unclear | Any of: BT.2020 primaries or matrix with an SDR or unset transfer; PQ on 8-bit video or with non-BT.2020 primaries; PQ with no first-frame probe (HDR10+ cannot be ruled out); stream and first frame disagree; Dolby Vision or HDR10+ metadata without a matching configuration or transfer; mastering metadata on SDR; a Dolby Vision codec tag (`dvh1`, `dvhe`, `dav1`, `dva1`) without a configuration record; an unrecognised transfer (for example `linear`) | Always skip |

"Reducing to HDR10" keeps the HDR10 base picture and drops the dynamic metadata or the Dolby Vision layer. It is a policy option, off by default, shown as "Allow HDR10+ and Dolby Vision to be reduced to HDR10". JellyTrim never tone-maps HDR to SDR.

HLG with Dolby Vision (profile 8.4) is treated as Dolby Vision without a usable base in the MVP.

## 3. Decisions (`internal/plan`)

`plan.Decide` runs after the policy engine chose an action. It returns one outcome with reasons:

| Outcome | Meaning |
|---|---|
| `Optimise` | An encode plan with an estimated size range |
| `AlreadyOptimal` | Nothing worth doing (with the reason) |
| `Protected` | A Protect policy matched |
| `Skipped` | JellyTrim cannot do this safely (with the reasons) |
| `NoPolicy` | No enabled policy matched |

Checks run in this order; the first skip stops the list, but all skip reasons found by the safety checks are reported together.

**Safety checks (skip)**
1. The file was not probed, or the probe failed.
2. Symlink, hard link (`nlink > 1`), or path outside the configured roots.
3. Container is not Matroska or MP4 (`mov,mp4,m4a,3gp,3g2,mj2` with an `.mp4` or `.m4v` extension).
4. More than one non-cover video stream; interlaced (`field_order` other than `progressive` or unknown); rotated.
5. HDR class that is always skipped, or needs an opt-in the policy does not give.
6. A stream the output container cannot hold (for example PGS in MP4), or a data stream in MP4.
7. No available encoder can produce the target codec (and, for HDR, preserve HDR metadata).

**Worth-it checks (already optimal)**
1. The file is in the `optimised` table (JellyTrim made it) and the action does not lower the resolution further.
2. Target resolution is not lower than the source, and the target codec is the same as the source codec or less efficient (efficiency order: AV1, HEVC, H.264, older). "Already in HEVC at this resolution."
   - Exception: if the source video bitrate is more than twice the upper estimate for the target, it is re-encoded: "HEVC at 38 Mbps is far above what High quality needs."
3. The estimated saving is below the minimum saving (default 10%).

**Target**
- Resolution: the policy's cap, never above the source. Scaled with `scale=-2:<height>` (or the width cap for wide films), keeping even dimensions and the sample aspect ratio.
- Codec: the policy's codec; "Keep existing" keeps the source codec (only a resolution change can then trigger an encode).
- Bit depth: 10-bit HEVC output by default (ADR 0006); always 10-bit for HDR.
- Audio, subtitles, attachments, chapters, metadata: copied.

### Size estimates

Before encoding, the estimate is a heuristic:

```
video_bps ≈ bits_per_pixel(codec, quality) × width × height × fps
estimate  = video_bps × duration + audio and subtitle bytes (copied)
range     = estimate × 0.7 to estimate × 1.3, capped at the source video size
```

Starting bits-per-pixel values for HEVC (10-bit, film content):

| Quality | bits per pixel |
|---|---|
| Maximum | 0.080 |
| High | 0.055 |
| Balanced | 0.038 |
| Space Saver | 0.026 |

H.264 uses 1.6 times these values; AV1 uses 0.8 times. These are rough and will be replaced by per-file sampling: `encoder.Sample` encodes three 20-second segments spread through the file and scales the result. The UI always shows estimates with `~` and the word "estimated".

## 4. Encoders (`internal/encoder`)

Each backend implements:

```go
type Backend interface {
    Name() string                 // "x265", "qsv-hevc"
    Codec() media.Codec           // HEVC
    Hardware() bool
    Probe(ctx, runner) Capability // real test encodes
    Args(p Plan) ([]string, error) // input options, filters, encoder options
}
```

**Auto** chooses, for the target codec, the first backend in the preference order (hardware first, by default) whose probe passed, and which can preserve HDR metadata if the source is HDR. Users can pick a backend explicitly, and can set Auto to prefer software.

### Quality maps

The numbers are not equivalent across encoders. They are starting values, to be calibrated with VMAF on test content before 1.0. Advanced settings can override them.

| Quality | x265 CRF | QSV HEVC `global_quality` (ICQ) |
|---|---|---|
| Maximum | 18 | 19 |
| High | 21 | 22 |
| Balanced | 24 | 25 |
| Space Saver | 27 | 28 |

- **x265** uses `-preset slow` by default (Advanced: medium, slow, slower). Lower CRF is higher quality. 18 is close to visually lossless for most film content; 27 is visibly softer but good for TV.
- **QSV** uses ICQ mode: `-global_quality N` without a bitrate. Its scale behaves differently from x265's CRF; the values are set a little higher than x265's CRF to give similar sizes. Look-ahead is off in the MVP.

### x265 arguments (software)

```
ffmpeg -nostdin -hide_banner -loglevel error -progress pipe:1 -nostats -y
  -i file:<source>
  -map 0:<main video> -map 0:a? -map 0:s? -map 0:t? [-map 0:<cover art>]...
  -map_metadata 0 -map_chapters 0
  -c copy
  -c:v:0 libx265 -preset slow -crf <N> -pix_fmt yuv420p10le -profile:v:0 main10
  -x265-params log-level=error:pools=<threads>[:hdr options]
  [-filter:v:0 scale=-2:<height>:flags=lanczos]
  -metadata:s:v:0 BPS= -metadata:s:v:0 NUMBER_OF_BYTES= ... (clear stale MKV statistics)
  -metadata JELLYTRIM=v1;enc=x265;q=<tier>
  -max_muxing_queue_size 4096
  -f matroska file:<dir>/.<name>.jellytrim-<job>.partial
```

For HDR10, the x265 parameters add `colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc:hdr10=1:hdr10-opt=1:repeat-headers=1`, plus `master-display=...` and `max-cll=...` built from the probed side data. For HLG: `transfer=arib-std-b67` and no static metadata. The same colour values are also set on the stream (`-color_primaries`, `-color_trc`, `-colorspace`).

For MP4 output: `-f mp4 -tag:v:0 hvc1 -movflags +faststart`.

### QSV arguments (Intel, Linux)

```
  -init_hw_device vaapi=va:/dev/dri/renderD128,driver=iHD
  -init_hw_device qsv=qs@va -filter_hw_device qs
  -hwaccel qsv -hwaccel_output_format qsv
  -i file:<source>
  ... same maps ...
  -c copy
  -c:v:0 hevc_qsv -preset slow -global_quality <N> -profile:v:0 main10
  -filter:v:0 vpp_qsv=w=<w>:h=<h>:format=p010
```

If the GPU cannot decode the source codec, the backend uses software decoding and uploads frames: `-filter:v:0 format=p010le,hwupload=extra_hw_frames=64,vpp_qsv=...`. HDR is sent to QSV only if its probe shows the output keeps the mastering display and content light level metadata; otherwise Auto uses x265 for HDR files.

The QSV backend is covered by golden argument tests. It has not been tested on real Intel hardware by the maintainers yet; reports are welcome.

### Hardware probe

For each backend, JellyTrim:
1. checks the encoder is listed by `ffmpeg -hide_banner -encoders`;
2. runs a real 1-second test encode of a `lavfi` test source with the exact options used for real jobs;
3. for HDR, encodes a short HDR10 test clip and checks the output's side data;
4. records the result, the ffmpeg version and, for Intel, the device name (from `vainfo` if available).

Results are shown in Settings and can be re-run.

## 5. Encoding safety

- The partial file is `.<name>.jellytrim-<job>.partial` in the same directory as the source. Jellyfin ignores dotfiles, so it never sees it.
- The muxer is always explicit (`-f matroska` or `-f mp4`).
- Progress comes from `-progress pipe:1` (`out_time_us`, `speed`, `total_size`). The duration comes from the format.
- **Early abort:** after 15% of the duration, if `total_size / fraction_done` is more than 90% of the source size, the encode stops and the job is Skipped: "The new file would be about the same size as the original."
- CPU is limited for software encodes (x265 `pools`, and the process runs with a lower priority). Encoding can be limited to a daily time window.

## 6. Validation

After encoding, JellyTrim probes the partial file and rejects it if any check fails:

1. It parses as the expected container.
2. The main video stream has the target codec, resolution (within 2 pixels) and bit depth.
3. Colour: the same primaries, transfer and matrix as the source for HDR; BT.709 or unset for SDR. For HDR10, mastering display and content light level metadata are present in the first frame.
4. The number of audio, subtitle and attachment streams equals the source's. Each stream keeps its codec (copied), language, title and default and forced flags, in the same order.
5. The chapters count equals the source's.
6. The duration is within 0.5 seconds or 0.5% of the source, whichever is larger.
7. A full decode of the output finishes with no errors (`ffmpeg -v error -i file:<partial> -map 0:v:0 -f null -`). Settings can reduce this to a sampled decode.
8. The saving is at least the minimum (default 10%).

A failure keeps the original, deletes the partial file, and records the failing check in plain words with the details.

## 7. Replacement

1. Check free space.
2. Copy the original's permission bits to the partial file. Try to set the same owner and group; if that fails, record a warning (Jellyfin may still read it through group or other permissions).
3. fsync the partial file.
4. Re-check the source's device, inode, size and modification time against the values when the job started. Any change: stop, delete the partial, mark Skipped ("The file changed while JellyTrim was working on it").
5. Hard-link the original to `.<name>.jellytrim-bak-<job>` (journalled). If the filesystem does not support hard links, rename the original to that name instead (journalled).
6. Rename the partial over the original (journalled). This is atomic within one filesystem. `EXDEV` is a hard failure; JellyTrim never copies and deletes. `EBUSY` is retried with backoff.
7. fsync the directory.
8. Record the new file's identity in `optimised`.
9. Ask Jellyfin to rescan the file (`POST /Library/Media/Updated` with the Jellyfin-side path), then poll the item until Jellyfin reports the new size or codec. Fall back to an item refresh if it has not changed after about 90 seconds.

The new file keeps the new modification time, so Jellyfin notices the change and re-reads it. Because the path is the same, Jellyfin keeps the same item, with its watched state and favourites.

### Backups and restore

Backups are kept for 7 days by default (Settings: 0 to 90; 0 deletes the backup once Jellyfin has picked up the change). History has a **Restore** button while the backup exists: it renames the backup over the current file (keeping the optimised file as a backup in turn) and asks Jellyfin to rescan. A daily task deletes expired backups; the journal records each deletion.

A hard-linked backup uses no extra space until the replacement: then the backup holds the original's data and the new file holds the new data, so free space must cover the new file.

## 8. Diagnostics

Every job stores, for the expandable technical details in History:
- the exact ffmpeg and ffprobe arguments (the Jellyfin key never appears in them);
- the last 200 lines of ffmpeg's stderr;
- the ffmpeg version and the backend's probe details;
- the validation results.

The user-facing summary is one or two plain sentences, for example: "Encoding failed at 38%. Intel QSV returned an encoder error."

## 9. Stream handling reference

| Stream | MKV output | MP4 output |
|---|---|---|
| AAC, AC-3, E-AC-3, MP3, Opus, FLAC | Copy | Copy (FLAC and Opus: copy, supported by ffmpeg's MP4 muxer) |
| DTS, DTS-HD MA, TrueHD | Copy | Skip file (not reliably supported in MP4) |
| SRT (`subrip`), ASS/SSA | Copy | Skip file |
| `mov_text` | n/a | Copy |
| PGS (`hdmv_pgs_subtitle`), VobSub (`dvd_subtitle`) | Copy | Skip file |
| Fonts and other attachments | Copy | Skip file |
| Cover art (`attached_pic`) | Copy | Copy |
| Data streams (`tmcd` and similar) | Skip file | Skip file |
| Chapters, global metadata | Copy | Copy |

Nothing is ever dropped silently. Later versions will add explicit, user-chosen rules (remove commentary tracks, keep only some languages, compress lossless audio), off by default.
