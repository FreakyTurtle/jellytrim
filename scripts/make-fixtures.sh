#!/usr/bin/env bash
# Generates JellyTrim's synthetic media fixtures with ffmpeg's test sources.
# Never real media.
#
#   dev/media/{movies,tv}/...          short clips for the dev stack and
#                                      integration tests (gitignored)
#   internal/media/testdata/probe/     ffprobe JSON for each clip (committed),
#                                      with paths rewritten to /media/...
#
# Usage: scripts/make-fixtures.sh [--probe-only | --media-only]
#
# --media-only makes the clips but leaves the committed probe JSON alone.
# CI uses it: the JSON is a test input, and a different ffmpeg build (even a
# patch release) changes encoder tags and sizes, so only a maintainer should
# regenerate it, then review the golden diffs.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
media="$root/dev/media"
probe_dir="$root/internal/media/testdata/probe"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

FFMPEG=${JELLYTRIM_FFMPEG:-ffmpeg}
FFPROBE=${JELLYTRIM_FFPROBE:-ffprobe}
command -v "$FFMPEG" >/dev/null || { echo "make-fixtures: ffmpeg not found" >&2; exit 1; }
command -v "$FFPROBE" >/dev/null || { echo "make-fixtures: ffprobe not found" >&2; exit 1; }

probe_only=0
media_only=0
case "${1:-}" in
  --probe-only) probe_only=1 ;;
  --media-only) media_only=1 ;;
  "") ;;
  *) echo "make-fixtures: unknown option $1" >&2; exit 2 ;;
esac

mkdir -p "$media/movies" "$media/tv" "$media/seed" "$probe_dir"

export SVT_LOG=1 # SVT-AV1 logs to stderr regardless of -loglevel
ff() { "$FFMPEG" -hide_banner -loglevel error -nostdin -y "$@"; }

# Common inputs. Durations are short so the whole set builds in seconds.
video() { # video <size> <seconds> [rate]
  echo -f lavfi -i "testsrc2=size=$1:rate=${3:-24}:duration=$2"
}
tone() { # tone <freq> <seconds> [channel layout]
  echo -f lavfi -i "sine=frequency=$1:duration=$2:sample_rate=48000"
}

# Subtitle sources.
cat >"$work/en.srt" <<'SRT'
1
00:00:00,200 --> 00:00:01,500
This is a test subtitle.
SRT
cat >"$work/en-forced.srt" <<'SRT'
1
00:00:00,500 --> 00:00:01,200
[Foreign dialogue]
SRT
cat >"$work/fr.srt" <<'SRT'
1
00:00:00,200 --> 00:00:01,500
Ceci est un sous-titre de test.
SRT
cat >"$work/anime.ass" <<'ASS'
[Script Info]
ScriptType: v4.00+
PlayResX: 1280
PlayResY: 720

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Default,IBM Plex Sans,48,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,2,0,2,10,10,10,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:00:00.20,0:00:01.50,Default,,0,0,0,,{\an8}Styled test subtitle
ASS
font="$root/internal/web/static/vendor/fonts/ibm-plex-sans-latin-400.woff2"

build() {
  [ "$probe_only" = 1 ] && return 0
  "$@"
}

m="$media/movies"
t="$media/tv"

# H.264 1080p at a high bitrate: the classic "inefficient" file. Constant
# bitrate with filler, so its size is the same with any x264 build and the
# queue tests' re-encode always clears the minimum saving.
mkdir -p "$m/Alpha (2019)"
build ff $(video 1920x1080 3) $(tone 440 3) -map 0:v -map 1:a \
  -c:v libx264 -preset veryfast -b:v 12M -minrate 12M -maxrate 12M -bufsize 12M \
  -x264-params nal-hrd=cbr -pix_fmt yuv420p \
  -c:a aac -b:a 128k -metadata:s:a:0 language=eng \
  "$m/Alpha (2019)/Alpha (2019).mkv"

# HEVC 1080p, already efficient.
mkdir -p "$m/Bravo (2020)"
build ff $(video 1920x1080 3) $(tone 440 3) -map 0:v -map 1:a \
  -c:v libx265 -preset ultrafast -crf 28 -x265-params log-level=error -tag:v hvc1 \
  -c:a aac -b:a 128k -metadata:s:a:0 language=eng \
  "$m/Bravo (2020)/Bravo (2020).mkv"

# H.264 2160p with 5.1 AC-3.
mkdir -p "$m/Charlie (2021)"
build ff $(video 3840x2160 1) -f lavfi -i "sine=frequency=220:duration=1:sample_rate=48000" \
  -map 0:v -map 1:a -c:v libx264 -preset ultrafast -b:v 20M -pix_fmt yuv420p \
  -c:a ac3 -b:a 448k -ac 6 -metadata:s:a:0 language=eng \
  "$m/Charlie (2021)/Charlie (2021).mkv"

# HEVC 2160p HDR10 with mastering display and content light level metadata.
mkdir -p "$m/Delta (2022)"
build ff $(video 3840x2160 1) $(tone 330 1) -map 0:v -map 1:a \
  -vf "setparams=color_primaries=bt2020:color_trc=smpte2084:colorspace=bt2020nc,format=yuv420p10le" \
  -c:v libx265 -preset ultrafast -crf 26 \
  -x265-params "log-level=error:hdr10=1:repeat-headers=1:colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc:master-display=G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,50):max-cll=1000,400" \
  -color_primaries bt2020 -color_trc smpte2084 -colorspace bt2020nc \
  -c:a eac3 -b:a 384k -metadata:s:a:0 language=eng \
  "$m/Delta (2022)/Delta (2022).mkv"

# HEVC 1080p HLG.
mkdir -p "$m/Echo (2022)"
build ff $(video 1920x1080 2) $(tone 330 2) -map 0:v -map 1:a \
  -vf "setparams=color_primaries=bt2020:color_trc=arib-std-b67:colorspace=bt2020nc,format=yuv420p10le" \
  -c:v libx265 -preset ultrafast -crf 26 \
  -x265-params "log-level=error:colorprim=bt2020:transfer=arib-std-b67:colormatrix=bt2020nc" \
  -color_primaries bt2020 -color_trc arib-std-b67 -colorspace bt2020nc \
  -c:a aac -metadata:s:a:0 language=eng \
  "$m/Echo (2022)/Echo (2022).mkv"

# AV1 1080p.
mkdir -p "$m/Foxtrot (2023)"
build ff $(video 1920x1080 2) $(tone 440 2) -map 0:v -map 1:a \
  -c:v libsvtav1 -preset 12 -crf 40 -svtav1-params log-level=0 -pix_fmt yuv420p10le \
  -c:a libopus -b:a 96k -metadata:s:a:0 language=eng \
  "$m/Foxtrot (2023)/Foxtrot (2023).mkv"

# H.264 1080p with three audio tracks and three subtitle tracks.
mkdir -p "$m/Golf (2018)"
build ff $(video 1920x1080 2) \
  -f lavfi -i "sine=frequency=200:duration=2:sample_rate=48000" \
  $(tone 600 2) $(tone 800 2) \
  -i "$work/en.srt" -i "$work/en-forced.srt" -i "$work/fr.srt" \
  -map 0:v -map 1:a -map 2:a -map 3:a -map 4:s -map 5:s -map 6:s \
  -c:v libx264 -preset veryfast -b:v 6M -pix_fmt yuv420p \
  -c:a:0 ac3 -ac:a:0 6 -b:a:0 448k -c:a:1 aac -c:a:2 aac -c:s srt \
  -metadata:s:a:0 language=eng -metadata:s:a:0 title="Surround 5.1" \
  -metadata:s:a:1 language=eng -metadata:s:a:1 title="Director's Commentary" \
  -metadata:s:a:2 language=fre \
  -metadata:s:s:0 language=eng -metadata:s:s:1 language=eng -metadata:s:s:1 title="Forced" \
  -metadata:s:s:2 language=fre \
  -disposition:a:0 default -disposition:a:1 comment -disposition:a:2 0 \
  -disposition:s:0 0 -disposition:s:1 forced -disposition:s:2 0 \
  "$m/Golf (2018)/Golf (2018).mkv"

# Anime-style: 720p, Japanese audio, ASS subtitles with an attached font.
mkdir -p "$m/Hotel (2017)"
build ff $(video 1280x720 2) $(tone 500 2) -i "$work/anime.ass" \
  -map 0:v -map 1:a -map 2:s -attach "$font" \
  -metadata:s:t mimetype=font/woff2 -metadata:s:t filename=IBMPlexSans.woff2 \
  -c:v libx264 -preset veryfast -b:v 3M -pix_fmt yuv420p -c:a aac -c:s ass \
  -metadata:s:a:0 language=jpn -metadata:s:s:0 language=eng -disposition:s:0 default \
  "$m/Hotel (2017)/Hotel (2017).mkv"

# Scope aspect ratio: 1920x800 counts as 1080p.
mkdir -p "$m/India (2016)"
build ff $(video 1920x800 2) $(tone 440 2) -map 0:v -map 1:a \
  -c:v libx264 -preset veryfast -b:v 6M -pix_fmt yuv420p -c:a aac -metadata:s:a:0 language=eng \
  "$m/India (2016)/India (2016).mkv"

# Interlaced H.264: must be skipped.
mkdir -p "$m/Juliet (2005)"
build ff $(video 1920x1080 2 25) $(tone 440 2) -map 0:v -map 1:a \
  -c:v libx264 -preset veryfast -flags +ilme+ildct -x264opts tff=1 -field_order tt -pix_fmt yuv420p \
  -c:a aac -metadata:s:a:0 language=eng \
  "$m/Juliet (2005)/Juliet (2005).mkv"

# MP4 with mov_text subtitles and cover art.
mkdir -p "$m/Kilo (2015)"
build ff -f lavfi -i "testsrc2=size=320x480:duration=1" -frames:v 1 "$work/cover.png"
build ff $(video 1920x1080 2) $(tone 440 2) -i "$work/en.srt" -i "$work/cover.png" \
  -map 0:v -map 1:a -map 2:s -map 3:v \
  -c:v:0 libx264 -preset veryfast -b:v 6M -pix_fmt yuv420p -c:v:1 png -disposition:v:1 attached_pic \
  -c:a aac -c:s mov_text -metadata:s:a:0 language=eng -metadata:s:s:0 language=eng \
  "$m/Kilo (2015)/Kilo (2015).mp4"

# Hard-linked file: a "seeding" copy outside the library shares the inode.
mkdir -p "$m/Lima (2014)"
build ff $(video 1920x1080 2) $(tone 440 2) -map 0:v -map 1:a \
  -c:v libx264 -preset veryfast -b:v 8M -pix_fmt yuv420p -c:a aac -metadata:s:a:0 language=eng \
  "$media/seed/Lima (2014).mkv"
if [ "$probe_only" = 0 ]; then
  rm -f "$m/Lima (2014)/Lima (2014).mkv"
  ln "$media/seed/Lima (2014).mkv" "$m/Lima (2014)/Lima (2014).mkv"
fi

# TV: an H.264 1080p show and an HEVC 720p show.
for ep in 01 02 03; do
  mkdir -p "$t/Mike Show/Season 01"
  build ff $(video 1920x1080 2) $(tone 440 2) -map 0:v -map 1:a \
    -c:v libx264 -preset veryfast -b:v 5M -pix_fmt yuv420p -c:a aac -metadata:s:a:0 language=eng \
    "$t/Mike Show/Season 01/Mike Show - S01E$ep.mkv"
done
for ep in 01 02; do
  mkdir -p "$t/November Show/Season 01"
  build ff $(video 1280x720 2) $(tone 440 2) -map 0:v -map 1:a \
    -c:v libx265 -preset ultrafast -crf 30 -x265-params log-level=error \
    -c:a aac -metadata:s:a:0 language=eng \
    "$t/November Show/Season 01/November Show - S01E$ep.mkv"
done

if [ "$media_only" = 1 ]; then
  echo "make-fixtures: clips in dev/media (probe JSON left as committed)"
  exit 0
fi

# ffprobe JSON, using the same arguments as internal/ffmpeg. The absolute
# path is replaced with the path Jellyfin would see, so nothing personal is
# committed.
probe() { # probe <file> <fixture name>
  local file=$1 name=$2 rel=${1#"$media"/}
  "$FFPROBE" -v error -show_format -show_streams -show_chapters -of json "file:$file" \
    | FIXTURE_PATH="/media/$rel" python3 -c '
import json, os, sys
d = json.load(sys.stdin)
d["format"]["filename"] = "file:" + os.environ["FIXTURE_PATH"]
json.dump(d, sys.stdout, indent=2, sort_keys=True)
sys.stdout.write("\n")' >"$probe_dir/$name.json"
  "$FFPROBE" -v error -select_streams v:0 -read_intervals '%+#1' -show_frames \
    -show_entries frame=color_transfer,color_primaries,color_space,side_data_list -of json "file:$file" \
    | python3 -c 'import json,sys; json.dump(json.load(sys.stdin), sys.stdout, indent=2, sort_keys=True); sys.stdout.write("\n")' \
    >"$probe_dir/$name.frames.json"
}

probe "$m/Alpha (2019)/Alpha (2019).mkv" h264-1080p
probe "$m/Bravo (2020)/Bravo (2020).mkv" hevc-1080p
probe "$m/Charlie (2021)/Charlie (2021).mkv" h264-2160p
probe "$m/Delta (2022)/Delta (2022).mkv" hevc-2160p-hdr10
probe "$m/Echo (2022)/Echo (2022).mkv" hevc-1080p-hlg
probe "$m/Foxtrot (2023)/Foxtrot (2023).mkv" av1-1080p
probe "$m/Golf (2018)/Golf (2018).mkv" multi-audio-subs
probe "$m/Hotel (2017)/Hotel (2017).mkv" anime-ass
probe "$m/India (2016)/India (2016).mkv" scope-1080p
probe "$m/Juliet (2005)/Juliet (2005).mkv" interlaced
probe "$m/Kilo (2015)/Kilo (2015).mp4" mp4-movtext-cover
probe "$t/Mike Show/Season 01/Mike Show - S01E01.mkv" tv-episode

echo "make-fixtures: clips in dev/media, probe JSON in internal/media/testdata/probe"
