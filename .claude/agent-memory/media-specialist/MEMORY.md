# media-specialist memory

This file is committed to a public repository. Never record personal paths, hostnames, IP addresses, email addresses, API keys or details of anyone's private infrastructure or media library.

Curate this file: keep it under 150 lines, one fact per bullet, date each entry (YYYY-MM-DD), and replace an outdated fact rather than adding a contradicting one.

- 2026-09-27: ffmpeg 8 takes stream colour tags (primaries, transfer) from frame properties negotiated through the filter graph. `-color_trc`/`-color_primaries` as output options were ignored after `-vf format=...`; the MKV stream then had only `color_space`. Set them with `setparams=color_primaries=..:color_trc=..:colorspace=..` in the filter chain, and verify the output stream tags in validation.
- 2026-09-27: ffprobe attachment streams (MKV fonts) can have no `codec_name`; only `tags.mimetype` and `tags.filename`. Parsers must not require codec_name.
- 2026-09-27: Local ffmpeg 8.1 wrote `field_order: bt` for an x264 encode asked for `tff=1`; treat any value other than `progressive`/`unknown`/missing as interlaced.
- 2026-09-27: SVT-AV1 ignores `-loglevel`; set `SVT_LOG=1` to quieten it.
- 2026-09-27: ffmpeg's MKV muxer writes no stream `bit_rate` for AAC or Opus audio (AC-3/E-AC-3 do report one); mkvmerge files carry BPS tags instead. media.VideoBitrate counts unknown lossy audio at 96 kbps/channel (cap 768k) so ffmpeg-muxed files still get a (slightly low) video estimate.
- 2026-09-27: ffprobe 8 writes the Display Matrix `rotation` as an integer; a `tmcd` data stream has no `codec_name`; BT.470 transfers print as `gamma22`/`gamma28`, not `bt470m`/`bt470bg`.
- 2026-09-27: ffmpeg 8 (fftools) marks the first stream of a type as default when the type has two or more streams and none is default, so copied subtitles gain a default flag. Matroska `-default_mode passthrough` does not stop it; any manual `-disposition` does. JellyTrim sets `-disposition:v:0` from the source's main video.
- 2026-09-27: The MP4 muxer drops global tags it does not know (JELLYTRIM). `-movflags +use_metadata_tags` keeps them but rewrites all metadata as QuickTime mdta keys and drops iTunes cover art (covr); fftools overwrites `encoder`. MP4 outputs therefore carry no JELLYTRIM tag.
- 2026-09-27: libx265 in ffmpeg 8.1 (x265 4.2) has `-dolbyvision` (default auto) and accepts `hdr10=1:hdr10-opt=1:repeat-headers=1`. master-display and max-cll in x265-params give exactly one Mastering display and one Content light level entry in the first frame, and the values round-trip exactly through ffprobe and media.Parse.
- 2026-09-27: x265 writes its settings in a User Data Unregistered SEI (for example `crf=21.0`); grepping the output for it proves a stream-scoped `-crf:v:0` was applied.
- 2026-09-27: `scale=W:H` with explicit dimensions changes the SAR to keep the exact display aspect (1920x800 to 1280x532 gives about 1596:1600). Follow it with `setsar=N/D`; inside a filter ':' separates options, so `setsar=1:1` is misparsed.
- 2026-09-27: setparams has no `gamma22`/`gamma28` names (ffprobe's names for BT.470 transfers); map them to `bt470m`/`bt470bg`.
- 2026-09-27: The `sidedata=mode=delete:type=...` filter takes DOVI_RPU_BUFFER, DOVI_METADATA and DYNAMIC_HDR_PLUS, which strips Dolby Vision and HDR10+ from decoded frames before any encoder sees them.
- 2026-09-27: media.Parse decides the container from the extension too, so a `.partial` output parses as `other`. Validation must compare FormatName.
