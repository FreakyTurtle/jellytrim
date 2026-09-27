# media-specialist memory

This file is committed to a public repository. Never record personal paths, hostnames, IP addresses, email addresses, API keys or details of anyone's private infrastructure or media library.

Curate this file: keep it under 150 lines, one fact per bullet, date each entry (YYYY-MM-DD), and replace an outdated fact rather than adding a contradicting one.

- 2026-09-27: ffmpeg 8 takes stream colour tags (primaries, transfer) from frame properties negotiated through the filter graph. `-color_trc`/`-color_primaries` as output options were ignored after `-vf format=...`; the MKV stream then had only `color_space`. Set them with `setparams=color_primaries=..:color_trc=..:colorspace=..` in the filter chain, and verify the output stream tags in validation.
- 2026-09-27: ffprobe attachment streams (MKV fonts) can have no `codec_name`; only `tags.mimetype` and `tags.filename`. Parsers must not require codec_name.
- 2026-09-27: Local ffmpeg 8.1 wrote `field_order: bt` for an x264 encode asked for `tff=1`; treat any value other than `progressive`/`unknown`/missing as interlaced.
- 2026-09-27: SVT-AV1 ignores `-loglevel`; set `SVT_LOG=1` to quieten it.
