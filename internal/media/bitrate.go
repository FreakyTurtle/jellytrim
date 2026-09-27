package media

import "math"

// BitrateSource says where a derived video bitrate came from, so
// explanations can say "from the stream" or "estimated from file size".
type BitrateSource string

// Bitrate sources, from most to least direct.
const (
	BitrateUnknown BitrateSource = ""
	// BitrateFromStream is the stream's bit_rate, or its MKV BPS tag.
	BitrateFromStream BitrateSource = "stream"
	// BitrateFromContainer is the container bitrate minus the other streams.
	BitrateFromContainer BitrateSource = "container"
	// BitrateFromSize is the file size over the duration, minus the other
	// streams.
	BitrateFromSize BitrateSource = "size"
)

// Label is the phrase shown to users after the bitrate.
func (s BitrateSource) Label() string {
	switch s {
	case BitrateFromStream:
		return "from the stream"
	case BitrateFromContainer:
		return "estimated from the container bitrate"
	case BitrateFromSize:
		return "estimated from file size"
	}
	return "unknown"
}

// VideoBitrate is the main video stream's bitrate in bits per second.
// known is false when it cannot be worked out; see VideoBitrateSource for
// the derivation.
func (f *File) VideoBitrate() (bps int64, known bool) {
	bps, src := f.videoBitrate()
	return bps, src != BitrateUnknown
}

// VideoBitrateSource says how VideoBitrate was derived, or BitrateUnknown.
func (f *File) VideoBitrateSource() BitrateSource {
	_, src := f.videoBitrate()
	return src
}

// videoBitrate tries, in order: the stream's own bitrate (bit_rate or BPS,
// folded together by Parse); the container bitrate minus the other streams;
// the file size over the duration minus the other streams.
//
// The two estimates subtract every audio stream and every other real video
// stream. Lossy audio without a stated bitrate is counted at a generous
// nominal rate (see nominalAudioBitrate); any other unknown stream makes the
// estimate refuse rather than guess. Subtitles,
// attachments, cover art and container overhead are not subtracted; they are
// small next to video, so the estimate runs slightly high.
func (f *File) videoBitrate() (int64, BitrateSource) {
	main, ok := f.MainVideo()
	if !ok {
		return 0, BitrateUnknown
	}
	if main.BitRate > 0 {
		return main.BitRate, BitrateFromStream
	}
	others, ok := f.otherStreamsBitrate(main.Index)
	if !ok {
		return 0, BitrateUnknown
	}
	if f.BitRate > 0 {
		if v := f.BitRate - others; v > 0 {
			return v, BitrateFromContainer
		}
		return 0, BitrateUnknown
	}
	if f.Size > 0 && f.Duration > 0 {
		total := int64(math.Round(float64(f.Size) * 8 / f.Duration.Seconds()))
		if v := total - others; v > 0 {
			return v, BitrateFromSize
		}
	}
	return 0, BitrateUnknown
}

// otherStreamsBitrate sums the audio streams and the real video streams
// other than the main one. ok is false when any of them is unknown and
// cannot be estimated safely.
//
// A lossy audio stream with no bitrate (ffmpeg's MKV muxer writes none for
// AAC or Opus) is counted at a generous nominal rate per channel. Counting it
// high makes the video estimate low, which is the cautious direction: a
// video bitrate that is too low can only make JellyTrim decide a file is
// already efficient. Lossless and unknown codecs are never guessed.
func (f *File) otherStreamsBitrate(mainIndex int) (int64, bool) {
	var sum int64
	for _, a := range f.Audio {
		switch {
		case a.BitRate > 0:
			sum += a.BitRate
		case nominalAudioBitrate(a) > 0:
			sum += nominalAudioBitrate(a)
		default:
			return 0, false
		}
	}
	for _, v := range f.Video {
		if v.Index == mainIndex || v.Disposition.AttachedPic {
			continue
		}
		if v.BitRate <= 0 {
			return 0, false
		}
		sum += v.BitRate
	}
	return sum, true
}

// nominalAudioBitrate is a high-side guess for lossy audio with no stated
// bitrate, or 0 when the codec is not one JellyTrim will guess for.
func nominalAudioBitrate(a AudioStream) int64 {
	ch := int64(a.Channels)
	if ch <= 0 {
		ch = 2
	}
	switch a.Codec {
	case "aac", "opus", "vorbis", "mp3", "mp2", "ac3", "eac3":
		return min(ch*96_000, 768_000)
	}
	return 0
}
