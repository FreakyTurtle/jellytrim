// Package media is JellyTrim's model of a media file: its container, streams,
// colour and HDR characteristics. It is built from ffprobe output (see
// Parse) and is pure: no I/O.
package media

import (
	"strconv"
	"strings"
	"time"
)

// Codec is an ffprobe codec name, for example "h264", "hevc" or "av1".
type Codec string

// Video codecs JellyTrim reasons about.
const (
	CodecH264  Codec = "h264"
	CodecHEVC  Codec = "hevc"
	CodecAV1   Codec = "av1"
	CodecVP9   Codec = "vp9"
	CodecMPEG2 Codec = "mpeg2video"
	CodecVC1   Codec = "vc1"
	CodecMPEG4 Codec = "mpeg4"
)

// Label is the name shown to users.
func (c Codec) Label() string {
	switch c {
	case CodecH264:
		return "H.264"
	case CodecHEVC:
		return "HEVC"
	case CodecAV1:
		return "AV1"
	case CodecVP9:
		return "VP9"
	case CodecMPEG2:
		return "MPEG-2"
	case CodecVC1:
		return "VC-1"
	case CodecMPEG4:
		return "MPEG-4"
	case "":
		return "Unknown"
	}
	return strings.ToUpper(string(c))
}

// Efficiency ranks codecs by compression efficiency: higher is better.
// Unknown and legacy codecs are 0.
func (c Codec) Efficiency() int {
	switch c {
	case CodecAV1:
		return 3
	case CodecHEVC, CodecVP9:
		return 2
	case CodecH264:
		return 1
	}
	return 0
}

// Resolution is a resolution class named by its nominal height (1080 for
// 1080p). Zero means unknown.
type Resolution int

// Resolution classes.
const (
	Res480  Resolution = 480
	Res576  Resolution = 576
	Res720  Resolution = 720
	Res1080 Resolution = 1080
	Res1440 Resolution = 1440
	Res2160 Resolution = 2160
	Res4320 Resolution = 4320
)

// ClassOf returns the resolution class of a frame, judged by width or
// height so letterboxed films count by their width (1920x800 is 1080p).
func ClassOf(width, height int) Resolution {
	switch {
	case width <= 0 || height <= 0:
		return 0
	case width >= 6400 || height >= 3600:
		return Res4320
	case width >= 3200 || height >= 1800:
		return Res2160
	case width >= 2240 || height >= 1260:
		return Res1440
	case width >= 1600 || height >= 900:
		return Res1080
	case width >= 1120 || height >= 630:
		return Res720
	case width >= 900 || height >= 500:
		return Res576
	}
	return Res480
}

// Label is the name shown to users, for example "1080p".
func (r Resolution) Label() string {
	if r == 0 {
		return "Unknown"
	}
	return strconv.Itoa(int(r)) + "p"
}

// Width is the nominal 16:9 width of the class.
func (r Resolution) Width() int {
	switch r {
	case Res4320:
		return 7680
	case Res2160:
		return 3840
	case Res1440:
		return 2560
	case Res1080:
		return 1920
	case Res720:
		return 1280
	case Res576:
		return 1024
	case Res480:
		return 854
	}
	return 0
}

// HDRClass is JellyTrim's classification of a video stream's dynamic range.
// See docs/TRANSCODING.md section 2.
type HDRClass string

// HDR classes.
const (
	SDR   HDRClass = "sdr"
	HDR10 HDRClass = "hdr10"
	HLG   HDRClass = "hlg"
	// HDR10Plus has SMPTE 2094-40 dynamic metadata on an HDR10 base.
	HDR10Plus HDRClass = "hdr10plus"
	// DolbyVisionHDR10 is Dolby Vision with an HDR10-compatible base layer
	// (profile 7, or profile 8 with compatibility ID 1).
	DolbyVisionHDR10 HDRClass = "dv-hdr10"
	// DolbyVision is Dolby Vision without a usable base layer (profile 5 and
	// others). Always skipped.
	DolbyVision HDRClass = "dv"
	// Unclear means the signals contradict each other or are missing. Always skipped.
	Unclear HDRClass = "unclear"
)

// Label is the name shown to users.
func (h HDRClass) Label() string {
	switch h {
	case SDR:
		return "SDR"
	case HDR10:
		return "HDR10"
	case HLG:
		return "HLG"
	case HDR10Plus:
		return "HDR10+"
	case DolbyVisionHDR10:
		return "Dolby Vision (HDR10 base)"
	case DolbyVision:
		return "Dolby Vision"
	case Unclear:
		return "Unclear HDR"
	}
	return "Unknown"
}

// IsHDR reports whether the class is anything other than SDR.
func (h HDRClass) IsHDR() bool { return h != SDR && h != "" }

// HDRInfo is the classification with the facts that led to it, for
// explanations.
type HDRInfo struct {
	Class HDRClass
	Facts []string
}

// Disposition holds ffprobe's stream disposition flags.
type Disposition struct {
	Default         bool
	Forced          bool
	HearingImpaired bool
	VisualImpaired  bool
	Comment         bool
	Original        bool
	AttachedPic     bool
}

// DolbyVisionConfig is the DOVI configuration record from ffprobe side data.
type DolbyVisionConfig struct {
	Profile           int
	Level             int
	RPU               bool
	EL                bool
	BL                bool
	BLCompatibilityID int
}

// MasteringDisplay is SMPTE ST 2086 static metadata, in x265 units:
// chromaticity in 0.00002 steps and luminance in 0.0001 cd/m².
type MasteringDisplay struct {
	RedX, RedY, GreenX, GreenY, BlueX, BlueY int
	WhiteX, WhiteY                           int
	MaxLuminance, MinLuminance               int
}

// ContentLight is the content light level metadata (MaxCLL and MaxFALL, cd/m²).
type ContentLight struct {
	MaxCLL  int
	MaxFALL int
}

// VideoStream is one video stream, including cover art (AttachedPic).
type VideoStream struct {
	Index       int
	Codec       Codec
	Profile     string
	CodecTag    string // codec_tag_string, for example "hvc1" or "dvh1"
	Width       int
	Height      int
	SAR         string // sample aspect ratio, "1:1"
	DAR         string
	FrameRate   float64
	FieldOrder  string
	BitDepth    int
	PixFmt      string
	Primaries   string
	Transfer    string
	Matrix      string
	Range       string
	BitRate     int64 // bits per second; 0 when the stream does not say
	Rotation    int
	Language    string
	Title       string
	Disposition Disposition
	Tags        map[string]string

	DolbyVision  *DolbyVisionConfig
	Mastering    *MasteringDisplay // from stream or first-frame side data
	ContentLight *ContentLight
	HDR10Plus    bool

	// Frame holds what the first-frame probe reported, or nil when it was
	// not run or returned no frame. Only the first video stream (v:0) is
	// probed.
	Frame *FrameInfo
}

// FrameInfo is the colour description and dynamic metadata signals of the
// first decoded frame. HDR classification compares it with the stream's own
// tags, and treats a missing frame probe as "HDR10+ cannot be ruled out".
type FrameInfo struct {
	Primaries string
	Transfer  string
	Matrix    string
	// DolbyVisionRPU is set when the frame carries Dolby Vision RPU side
	// data, even if the stream has no DOVI configuration record.
	DolbyVisionRPU bool
}

// Interlaced reports whether the stream is interlaced. Unknown counts as
// progressive; any explicit field order other than progressive does not.
func (v VideoStream) Interlaced() bool {
	switch v.FieldOrder {
	case "", "progressive", "unknown":
		return false
	}
	return true
}

// Resolution is the stream's resolution class.
func (v VideoStream) Resolution() Resolution { return ClassOf(v.Width, v.Height) }

// AudioStream is one audio stream.
type AudioStream struct {
	Index       int
	Codec       string
	Profile     string // for example "DTS-HD MA" or "TrueHD + Atmos"
	Channels    int
	Layout      string
	SampleRate  int
	BitRate     int64
	Language    string // "" when unknown
	Title       string
	Disposition Disposition
}

// Commentary reports whether the stream looks like a commentary track.
func (a AudioStream) Commentary() bool {
	return a.Disposition.Comment || strings.Contains(strings.ToLower(a.Title), "commentary")
}

// SubtitleStream is one subtitle stream.
type SubtitleStream struct {
	Index       int
	Codec       string // subrip, ass, hdmv_pgs_subtitle, dvd_subtitle, mov_text, ...
	Language    string
	Title       string
	Disposition Disposition
}

// Bitmap reports whether the subtitle format is image-based.
func (s SubtitleStream) Bitmap() bool {
	switch s.Codec {
	case "hdmv_pgs_subtitle", "dvd_subtitle", "dvb_subtitle", "xsub":
		return true
	}
	return false
}

// Attachment is an attached file, usually a font for ASS subtitles.
type Attachment struct {
	Index    int
	Codec    string // often empty for fonts
	MimeType string
	FileName string
}

// DataStream is a non-media stream, such as an MP4 timecode track.
type DataStream struct {
	Index    int
	Codec    string
	CodecTag string
}

// Container is the output-relevant container family.
type Container string

// Containers.
const (
	Matroska       Container = "matroska"
	MP4            Container = "mp4"
	OtherContainer Container = "other"
)

// File is a probed media file.
type File struct {
	Path        string
	Size        int64 // bytes
	Duration    time.Duration
	FormatName  string // ffprobe format_name, for example "matroska,webm"
	Container   Container
	BitRate     int64 // overall, bits per second; 0 when unknown
	Tags        map[string]string
	Chapters    int
	StreamCount int

	Video       []VideoStream // includes cover art; see MainVideo
	Audio       []AudioStream
	Subtitles   []SubtitleStream
	Attachments []Attachment
	Data        []DataStream
}

// MainVideo returns the first video stream that is not cover art.
func (f *File) MainVideo() (VideoStream, bool) {
	for _, v := range f.Video {
		if !v.Disposition.AttachedPic {
			return v, true
		}
	}
	return VideoStream{}, false
}

// RealVideoCount counts video streams that are not cover art.
func (f *File) RealVideoCount() int {
	n := 0
	for _, v := range f.Video {
		if !v.Disposition.AttachedPic {
			n++
		}
	}
	return n
}

// CoverArt returns the attached-picture streams.
func (f *File) CoverArt() []VideoStream {
	var out []VideoStream
	for _, v := range f.Video {
		if v.Disposition.AttachedPic {
			out = append(out, v)
		}
	}
	return out
}

// JellyTrimTag returns the JELLYTRIM container tag written on files JellyTrim
// produced, or "".
func (f *File) JellyTrimTag() string {
	for k, v := range f.Tags {
		if strings.EqualFold(k, "JELLYTRIM") {
			return v
		}
	}
	return ""
}
