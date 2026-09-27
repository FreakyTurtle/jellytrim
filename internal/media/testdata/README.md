# internal/media test data

## `probe/`

ffprobe 8 JSON, one pair per case:

- `<name>.json`: `ffprobe -v error -show_format -show_streams -show_chapters -of json`
- `<name>.frames.json`: the first-frame probe of `v:0` (`-show_frames -show_entries frame=color_transfer,color_primaries,color_space,side_data_list`)

Paths are always fake `/media/movies/...` or `/media/tv/...` paths. Nothing here comes from a real library.

### Generated

`scripts/make-fixtures.sh` builds synthetic clips with ffmpeg's test sources and probes them. Do not edit these by hand; run the script (or `task dev:fixtures`) and review the golden diff.

`anime-ass`, `av1-1080p`, `h264-1080p`, `h264-2160p`, `hevc-1080p`, `hevc-1080p-hlg`, `hevc-2160p-hdr10`, `interlaced`, `mp4-movtext-cover`, `multi-audio-subs`, `scope-1080p`, `tv-episode`.

`av1-1080p` is 10-bit with no colour tags. It is classified SDR ("10-bit video without colour tags or HDR metadata"), the case that is common in real SDR encodes.

### Hand-maintained

`make-fixtures.sh` does not produce these. Edit them by hand, keeping the ffprobe 8 shape (keys sorted, two-space indent) and fake paths.

| Fixture | How it was made | Why it is not generated |
|---|---|---|
| `rotated` | hand-written: probed from a local ffmpeg 8.1 clip remuxed with `-display_rotation -90`, path rewritten | The script has no rotated clip. Covers the Display Matrix `rotation: -90` side data. |
| `multi-video` | hand-written: probed from a local ffmpeg 8.1 MKV with two real video streams, path rewritten | The script has no multi-video clip. Covers "more than one real video stream" and a second video with unknown bitrate. |
| `data-stream-mp4` | hand-written: probed from a local ffmpeg 8.1 MP4 made with `-timecode 01:00:00:00 -write_tmcd 1`, path rewritten | The script has no timecode track. Covers a `tmcd` data stream with no `codec_name`. |
| `pgs-subtitles` | hand-written, modelled on mkvmerge output | ffmpeg cannot encode PGS. Covers `hdmv_pgs_subtitle` (default, forced, hearing impaired, German), mkvmerge `BPS` statistics tags, a `BPS-eng` tag, DTS-HD MA, a commentary track, BT.709 tags and chapters. |
| `dv-profile5` | hand-written | ffmpeg cannot encode Dolby Vision. MP4 with `dvh1`, DOVI record profile 5 compatibility 0, no colour tags, Dolby Vision RPU in the frame. Must be `dv` (always skipped). |
| `dv-profile8-hdr10` | hand-written | Dolby Vision profile 8.1 (compatibility 1), PQ, BT.2020, with mastering display and content light level in the **stream** side data, TrueHD Atmos. Must be `dv-hdr10`. |
| `dv-profile7` | hand-written | Dolby Vision profile 7 with an enhancement layer (compatibility 6), PQ, BT.2020. Must be `dv-hdr10`. |
| `hdr10plus` | hand-written | Needs an x265 build with HDR10+ support and a metadata file. PQ, BT.2020, frame side data `HDR Dynamic Metadata SMPTE2094-40 (HDR10+)`. Must be `hdr10plus`. |
| `unclear-10bit` | hand-written | 10-bit HEVC with a BT.2020 matrix (`bt2020nc`) but no transfer or primaries: HDR whose tags were lost. Must be `unclear`. |
| `unknown-bitrate` | hand-written | An MKV with no stream `bit_rate`, no `BPS` tags, no format `bit_rate` and no duration, so every bitrate derivation fails. |

## `golden/`

One `<name>.golden` per probe fixture: a text summary of the parsed model (container, duration, every stream, video bitrate and its source, resolution class, HDR class and facts). Regenerate with:

```sh
go test ./internal/media/ -run TestParseGolden -update
```

then read the whole diff before committing.
