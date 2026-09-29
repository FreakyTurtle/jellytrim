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
7. No available encoder can produce the target codec (and, for HDR, preserve HDR metadata). Reducing Dolby Vision or HDR10+ to HDR10 needs x265; a policy that names a hardware encoder for such a file is skipped with the reason.

**Worth-it checks (already optimal)**
1. The file is in the `optimised` table (JellyTrim made it) and the action does not lower the resolution further.
2. Target resolution is not lower than the source, and the target codec is the same as the source codec or less efficient (efficiency order: AV1, HEVC, H.264, older). "Already in HEVC at this resolution."
   - Exception: if the source video bitrate is more than twice the upper estimate for the target, it is re-encoded: "HEVC at 38 Mbps is far above what High quality needs."
3. The estimated saving is below the minimum saving (default 10%).

**Target**
- Resolution: the policy's cap, never above the source. The plan computes an explicit even width and height that fit inside the cap's nominal frame (for example 1920x800 for a scope film capped at 1080p), and the encoder scales with `scale=W:H:flags=lanczos,setsar=<source SAR>`.
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

Each backend (`x265`, `qsv-hevc`) implements `encoder.Backend`: its name and label, codec, whether it is hardware, the native quality number for each tier, a `Check` that refuses jobs it cannot do safely, and the three parts of its arguments (input options, the `-filter:v:0` chain, the encoder options). `encoder.BuildArgs(backend, job)` assembles the full argument list. Backends carry their detected `Capability`, so `BuildArgs` refuses HDR on a backend that has not proved it keeps HDR metadata.

**Auto** chooses, for the target codec, the first backend in the preference order (hardware first, by default) whose probe passed, and which can preserve HDR metadata if the source is HDR. Reducing Dolby Vision or HDR10+ to HDR10 always uses x265, because only libx265 has the `-dolbyvision 0` option. Users can pick a backend explicitly, and can set Auto to prefer software.

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
  -map 0:<main video> -map 0:<each other stream, in source order>
  -map_metadata 0 -map_chapters 0
  -c copy
  -c:v:0 libx265 -preset:v:0 slow -crf:v:0 <N> -profile:v:0 main10
  -x265-params:v:0 log-level=error[:hdr options]
  -filter:v:0 [scale=W:H:flags=lanczos,setsar=<source SAR>,]format=yuv420p10le[,setparams=<colour>]
  [-color_primaries:v:0 .. -color_trc:v:0 .. -colorspace:v:0 ..]
  -disposition:v:0 <source video disposition>
  -metadata:s:v:0 BPS= ... (clear stale MKV statistics, Matroska only)
  -metadata JELLYTRIM=v1;enc=x265;q=<tier>          (Matroska only)
  -max_muxing_queue_size 4096
  -f matroska file:<dir>/.<name>.jellytrim-<job>.partial
```

Notes from real encodes with ffmpeg 8:

- Every stream is mapped by index; nothing is mapped by type wildcard.
- Every encoder option is scoped to the output video stream (`:v:0`), so copied streams are untouched.
- ffmpeg 8 takes colour tags from frame properties negotiated through the filter graph, so tagged sources get `setparams=color_primaries=..:color_trc=..:colorspace=..` at the end of the chain as well as the `-color_*` options. Untagged sources stay untagged. Colour names come from an allow-list.
- After scaling, `setsar` restores the source's sample aspect ratio.
- `-disposition:v:0` is always set from the source. Without any explicit disposition, ffmpeg 8 marks the first subtitle track as default when none was, which would make players show it.

For HDR10, the x265 parameters add `colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc:hdr10=1:hdr10-opt=1:repeat-headers=1`, plus `master-display=...` and `max-cll=...` built from the probed side data. For HLG: `transfer=arib-std-b67` and no static metadata. When a policy allows reducing Dolby Vision or HDR10+ to HDR10, the chain first removes that metadata from the decoded frames and `-dolbyvision:v:0 0` is passed.

For MP4 output: `-f mp4 -tag:v:0 hvc1 -movflags +faststart`. The MP4 muxer drops unknown tags such as `JELLYTRIM` (and `-movflags +use_metadata_tags`, which would keep it, loses the cover art), so JellyTrim recognises its own MP4 files only through the `optimised` table.

### QSV arguments (Intel, Linux)

```
  -init_hw_device vaapi=va:/dev/dri/renderD128,driver=iHD
  -init_hw_device qsv=qs@va -filter_hw_device qs
  -hwaccel qsv -hwaccel_output_format qsv
  -i file:<source>
  ... same maps and copies ...
  -c:v:0 hevc_qsv -preset:v:0 slow -global_quality:v:0 <N> -profile:v:0 main10
  -filter:v:0 vpp_qsv=w=<w>:h=<h>:format=p010
```

If the GPU cannot decode the source (the hardware test records which codecs and bit depths it decoded), the backend drops `-hwaccel` and uploads software-decoded frames: `-filter:v:0 format=p010le,hwupload=extra_hw_frames=64,vpp_qsv=...`, keeping `-init_hw_device` and `-filter_hw_device`. HDR is sent to QSV only if its test shows the output keeps the mastering display and content light level metadata; otherwise Auto uses x265 for HDR files.

The QSV backend is covered by golden argument tests. It has not been tested on real Intel hardware by the maintainers yet; reports are welcome.

### Hardware probe

For each backend, JellyTrim:
1. checks the encoder is listed by `ffmpeg -hide_banner -encoders` (for QSV, first that `/dev/dri/renderD128` exists);
2. runs a real 1-second test encode of a `lavfi` test source with the exact rate-control options used for real jobs;
3. for HDR, re-encodes a short HDR10 clip made with x265 and checks the output's first-frame side data still has the mastering display and content light level metadata;
4. for QSV, decodes small clips per codec and bit depth to learn which sources the GPU can decode;
5. records the result and the ffmpeg version.

Results are stored, shown in Settings, and re-run at start-up and on request.

## 5. Encoding safety

- The partial file is `.<name>.jellytrim-<job>.partial` in the same directory as the source. Jellyfin ignores dotfiles, so it never sees it.
- The muxer is always explicit (`-f matroska` or `-f mp4`).
- Progress comes from `-progress pipe:1` (`out_time_us`, `speed`, `total_size`). The duration comes from the format.
- **Early abort:** after 15% of the duration (and at least 2 minutes of it, so headers and the first keyframes do not skew the projection), if `total_size / fraction_done` is more than 90% of the source size, the encode stops and the job is Skipped: "The new file would be about the same size as the original."
- CPU is limited for software encodes (x265 `pools`, and the process runs with a lower priority). Encoding can be limited to chosen hours of the week; when an hour ends, a running encode stops, its partial file is deleted, and the job waits for the next active hour.

## 6. Validation

After encoding, JellyTrim probes the partial file and rejects it if any check fails:

1. It parses as the expected container.
2. The main video stream has the target codec, resolution (within 2 pixels) and bit depth.
3. Colour: the same primaries, transfer and matrix as the source for HDR; BT.709 or unset for SDR. For HDR10, mastering display and content light level metadata are present in the first frame.
4. The number of audio, subtitle and attachment streams equals the source's. Each stream keeps its codec (copied), language, title and default and forced flags, in the same order.
5. The chapters count equals the source's.
6. The duration is within 0.5 seconds or 0.5% of the source, whichever is larger.
7. The encode itself logged nothing at error level (ffmpeg errors on a clean exit, such as undecodable source frames, reject the output); the main video stream's own duration matches the source's where both files record it (Matroska `DURATION` tags); and a full decode of the output finishes with no errors (`ffmpeg -v error -i file:<partial> -map 0:v:0 -f null -`). Settings can reduce this to a sampled decode.
8. The saving is at least the minimum (default 10%).

A failure keeps the original, deletes the partial file, and records the failing check in plain words with the details.

## 7. Replacement

Once the new file has passed validation, JellyTrim first waits until nobody is playing the file in Jellyfin, then confirms the job should still go ahead (see [Files being played](#files-being-played) and [The last checks](#the-last-checks)). Then:

1. Check free space.
2. Copy the original's permission bits to the partial file. Try to set the same owner and group; if that fails, record a warning (Jellyfin may still read it through group or other permissions).
3. fsync the partial file.
4. Re-check the source's device, inode, size and modification time against the values when the job started. Any change: stop, delete the partial, mark Skipped ("The file changed while JellyTrim was working on it"). Re-check the partial file's device, inode, size and modification time against the values recorded when it passed validation, before its permissions are touched. Any change: stop, delete the partial, mark Failed ("The new file changed after JellyTrim checked it").
5. Hard-link the original to `.<name>.jellytrim-bak-<job>` (journalled). If the filesystem does not support hard links, rename the original to that name instead (journalled).
6. Rename the partial over the original (journalled). This is atomic within one filesystem. `EXDEV` is a hard failure; JellyTrim never copies and deletes. `EBUSY` is retried with backoff.
7. fsync the directory.
8. Record the new file's identity in `optimised`.
9. Ask Jellyfin to rescan the file (`POST /Library/Media/Updated` with the Jellyfin-side path), then poll the item until Jellyfin reports the new size or codec. Fall back to an item refresh if it has not changed after about 90 seconds.

The new file keeps the new modification time, so Jellyfin notices the change and re-reads it. Because the path is the same, Jellyfin keeps the same item, with its watched state and favourites.

### Files being played

JellyTrim does not replace a file while someone is watching it in Jellyfin.

- **Before encoding.** When a job's turn comes, JellyTrim first asks Jellyfin whether the file is playing. If it is, the job is not started: it goes back to Waiting with a note such as "Not started: alex is watching this on Living Room TV. JellyTrim will try again in 30 min; other jobs go first.", and the queue moves on to the next job. The item is held back for 30 minutes. This avoids spending an encode on a file that could not be replaced yet. If Jellyfin cannot be reached at this point, the job starts; the check before replacing still applies. If Jellyfin rejects the API key or no connection is set up, the job is held back for 30 minutes instead ("Not started: Jellyfin rejected JellyTrim's API key or is not set up..."), because the check before replacing could never confirm the file is free.
- **Fresh watch state at the start.** Before re-checking the file, the job reads the item's watch state (played, play count, favourite, last played) from Jellyfin again, for the same users sync reads, so the decision does not rest on a sync that may be hours old. If Jellyfin cannot answer, the job carries on with the state from the last sync.
- **The check before replacing.** When the new file has passed validation, and before the original is re-checked and backed up, JellyTrim asks Jellyfin what is playing (`GET /Sessions?activeWithinSeconds=960`, the same filter Jellyfin's dashboard uses). A session counts if its playing item, or the version it is playing, is the job's item. A paused session counts as watching: the viewer may press play at any moment.
- **Waiting.** While the item is playing, the job stays in Replacing with a note in the queue, for example "Waiting: alex is watching this on Living Room TV". JellyTrim asks again every 30 seconds. The new file waits, hidden, beside the original. The job keeps its worker slot, so with one worker no other job starts meanwhile. The original and the new file are both re-checked after the wait, so a file that changed during it is not used.
- **Two answers once someone was watching.** If the item was seen playing during the wait, one "not playing" answer is not enough: JellyTrim needs two in a row, 30 seconds apart. A player moving to the next episode, or restarting a stream, can briefly drop out of the session list. An error from Jellyfin between the two starts the count again.
- **Giving up.** If someone is still watching after 6 hours, JellyTrim deletes the new file and puts the job back in the queue with the note "Someone was still watching this after 6 hours, so JellyTrim will try again later. The original is unchanged." The item is then held back for 2 hours, so a client left paused for days does not cause an endless loop of encodes; when it is picked up again, the check before encoding applies.
- **Giving up for good.** The third time a job gives up, it ends as Skipped: "Someone was watching this each time it was ready, so JellyTrim stopped trying. Queue it again to retry. The original is unchanged." The count is kept in memory, so a restart starts it again.
- **Holds are kept in memory.** After a restart, held-back items may be picked up straight away; the check before encoding catches any that are still playing.
- **When Jellyfin cannot be asked.** If Jellyfin does not answer (it is down or restarting), JellyTrim keeps asking every 30 seconds for up to 10 minutes. After that it replaces the file anyway and records a warning in the job's details: "Jellyfin could not be asked whether the file was playing; it was replaced anyway." This is safe for the file: the replacement is an atomic rename, and on Linux a player that already has the file open keeps reading the old copy until it closes it. A Jellyfin that cannot answer is also very unlikely to be streaming. The risk is only that a viewer who seeks or restarts playback lands in the new file.
- **When JellyTrim cannot confirm.** JellyTrim never goes ahead unchecked in two cases: when the item was seen playing earlier in the same wait, and when Jellyfin rejects the API key or no Jellyfin connection is set up (those do not clear up by waiting a few minutes). It keeps asking until the 6-hour limit, then gives up as above: "JellyTrim could not confirm with Jellyfin that nobody was watching this after 6 hours, so it will try again later. The original is unchanged."
- **The processing schedule.** Waiting is not encoding. When an active hour ends, a job waiting for playback is not stopped: its encode is done and it uses no encoder while it waits. It finishes the replacement whenever the viewer stops, even outside active hours.
- **Cancel and shutdown.** Cancelling a waiting job, or stopping JellyTrim, deletes the new file and leaves the original unchanged. After a shutdown the job goes back to Waiting. Once the last checks have passed and the replacement has begun, Cancel is refused ("Too late to cancel: JellyTrim is replacing the file now. The original is kept as a backup.") and the queue no longer offers it: the remaining steps take moments and are journalled.
- **Restore.** Restore refuses while Jellyfin reports someone playing the item ("Someone is watching this right now (alex on Living Room TV). Try again when they have finished."). When Jellyfin cannot be asked, it refuses too ("JellyTrim could not check with Jellyfin whether this is playing. Try again in a moment."), and History offers **Restore anyway**, a second step with a confirmation, for when Jellyfin stays down while a backup is about to expire. Restore anyway skips only that check: if Jellyfin answers and reports playback, it still refuses. Retrying a job is not affected: its check happens when the new file is ready.

### The last checks

After any wait, and before the original is touched, the job confirms it should still go ahead with what is true now. Each check runs every time, whether or not there was a wait.

1. **Dry Run.** Settings are read again. If Dry Run is on, the new file is deleted and the job goes back to Waiting: "Not replaced: Dry Run was turned on, so the new file was discarded and nothing was changed. The job runs again when Dry Run is off." Nothing starts in Dry Run, so it stays there until Dry Run is turned off. If the settings cannot be read, the job goes back to the queue.
2. **Fresh watch state.** The item's watch state is read from Jellyfin again (as at the start), because someone may have watched or favourited it during a long encode, not only during the wait. If that fails and someone was seen playing the item during the wait, JellyTrim cannot tell what the viewer changed, so it gives up as if the viewer were still watching: the new file is deleted, the job goes back to the queue, the item is held back, and it counts towards the three give-ups. If it fails and nobody was seen, the state read at the job's start is used.
3. **Still chosen.** The item's decision is made again: the original is probed, and the current policies, exclusions and stored watch state are applied. The job goes ahead only if the policies still choose the same plan (codec, resolution, quality, encoder, container, streams and colour). Otherwise the new file is deleted and the job ends as Skipped: "Not replaced: someone watched this while it was being converted, and the policies no longer choose it." after a wait, or "Not replaced: the policies no longer choose this item (the reason)." when a policy or exclusion changed. If the original changed, the job is Skipped as in step 4 of the replacement. If the decision cannot be made (the file cannot be probed), the job is Skipped too.
4. **Dry Run again,** in case it was turned on while the file was re-checked.
5. **Playing again.** The checks above take a few seconds, so Jellyfin is asked once more. If someone has just started watching, the new file is deleted, the job goes back to the queue ("Someone started watching this just before it was replaced, so JellyTrim will try again later.") and the item is held back for 30 minutes. An error at this point is ignored, because Jellyfin answered moments earlier.

Then the job is committed: Cancel is refused from here on.

If the new file cannot be deleted after a give-up or any of these checks, the job ends as Failed, not back in the queue, and the summary names the file: "The new file was not used, but JellyTrim could not delete it. Delete .<name>.jellytrim-<job>.partial yourself. The original is unchanged."

### Backups and restore

Backups are kept for 7 days by default (Settings: 0 to 90; 0 deletes the backup once Jellyfin has picked up the change). History has a **Restore** button while the backup exists. Restore only goes ahead if the file at the path is still the one JellyTrim produced (same device, inode and size as recorded when the job finished), no job is working on the item, and Jellyfin confirms nobody is playing it (or the user chose **Restore anyway** when Jellyfin could not answer); otherwise it refuses, so a newer download is never overwritten. It renames the backup over the optimised file, asks Jellyfin to rescan, and marks the item so JellyTrim leaves it alone until the user allows changes again. A daily task deletes expired backups; the journal records each deletion.

A hard-linked backup uses no extra space until the replacement: then the backup holds the original's data and the new file holds the new data, so free space must cover the new file.

### When something goes wrong during replacement

- The original is re-checked after the backup is made and before every rename attempt. If another program replaced the file meanwhile, JellyTrim leaves the new file alone, keeps the version it started from as a backup, and marks the job Skipped.
- A rename that reports an error but did happen (a lost reply on a network filesystem) is detected by the new file's inode and treated as complete.
- If the final rename fails and the original cannot be put back either, the job is marked **Needs attention** with the exact location of the original. Nothing is deleted. Recovery tries again on every start.
- Recovery acts only on this job's exact partial and backup names next to the original.

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
