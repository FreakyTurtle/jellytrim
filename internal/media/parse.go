package media

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path"
	"sort"
	"strings"
	"time"
)

// ErrEmptyProbe is returned when ffprobe output has neither a format nor any
// streams, which means ffprobe could not read the file.
var ErrEmptyProbe = errors.New("ffprobe output has no format or streams")

// Parse builds a File from ffprobe output. probeJSON is the output of
// `ffprobe -show_format -show_streams -show_chapters -of json`; framesJSON is
// the first-frame probe of v:0 and may be empty. size is the file size in
// bytes as the caller measured it; when it is not positive the size ffprobe
// reported is used instead.
func Parse(probeJSON, framesJSON []byte, size int64) (*File, error) {
	var doc probeDoc
	if err := json.Unmarshal(probeJSON, &doc); err != nil {
		return nil, fmt.Errorf("parsing ffprobe stream output: %w", err)
	}
	if doc.Format.FormatName == "" && len(doc.Streams) == 0 {
		return nil, ErrEmptyProbe
	}
	frame, err := parseFirstFrame(framesJSON)
	if err != nil {
		return nil, err
	}

	f := fileFromFormat(doc.Format, size)
	f.Chapters = countChapters(doc.Chapters)
	f.StreamCount = len(doc.Streams)
	for _, s := range doc.Streams {
		addStream(f, s)
	}
	if frame != nil && len(f.Video) > 0 {
		applyFrame(&f.Video[0], frame)
	}
	return f, nil
}

// parseFirstFrame returns the first frame of the frame probe, or nil when
// the probe is empty or has no frames. Malformed JSON is an error: without
// it JellyTrim could not rule out HDR10+ or Dolby Vision signals.
func parseFirstFrame(framesJSON []byte) (*probeFrame, error) {
	if len(bytes.TrimSpace(framesJSON)) == 0 {
		return nil, nil
	}
	var doc framesDoc
	if err := json.Unmarshal(framesJSON, &doc); err != nil {
		return nil, fmt.Errorf("parsing ffprobe frame output: %w", err)
	}
	if len(doc.Frames) == 0 {
		return nil, nil
	}
	return &doc.Frames[0], nil
}

func fileFromFormat(pf probeFormat, size int64) *File {
	p := strings.TrimPrefix(pf.Filename, "file:")
	if size <= 0 {
		size, _ = pf.Size.Int64()
	}
	f := &File{
		Path:       p,
		Size:       size,
		FormatName: pf.FormatName,
		Container:  containerOf(pf.FormatName, p),
		Tags:       pf.Tags,
	}
	if secs, ok := pf.Duration.Float(); ok && secs > 0 {
		f.Duration = time.Duration(math.Round(secs * float64(time.Second)))
	}
	if br, ok := pf.BitRate.Int64(); ok && br > 0 {
		f.BitRate = br
	}
	return f
}

// containerOf maps ffprobe's format name to a container family. The
// extension must agree: ffprobe reports the same format name for .mov and
// .mp4, and for .webm and .mkv, but only .mp4/.m4v and .mkv can take
// JellyTrim's output unchanged.
func containerOf(formatName, filePath string) Container {
	ext := strings.ToLower(path.Ext(filePath))
	switch {
	case strings.Contains(formatName, "matroska") && ext == ".mkv":
		return Matroska
	case formatName == "mov,mp4,m4a,3gp,3g2,mj2" && (ext == ".mp4" || ext == ".m4v"):
		return MP4
	}
	return OtherContainer
}

func countChapters(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var chapters []json.RawMessage
	if err := json.Unmarshal(raw, &chapters); err != nil {
		return 0
	}
	return len(chapters)
}

// addStream files the stream under its codec type. Unknown types are kept as
// data streams so that nothing ffprobe reported goes unaccounted for.
func addStream(f *File, s probeStream) {
	switch s.CodecType {
	case "video":
		f.Video = append(f.Video, videoFrom(s))
	case "audio":
		f.Audio = append(f.Audio, audioFrom(s))
	case "subtitle":
		f.Subtitles = append(f.Subtitles, SubtitleStream{
			Index:       s.Index,
			Codec:       s.CodecName,
			Language:    language(s.Tags),
			Title:       tag(s.Tags, "title"),
			Disposition: dispositionOf(s.Disposition),
		})
	case "attachment":
		f.Attachments = append(f.Attachments, Attachment{
			Index:    s.Index,
			Codec:    s.CodecName,
			MimeType: tag(s.Tags, "mimetype"),
			FileName: tag(s.Tags, "filename"),
		})
	default:
		f.Data = append(f.Data, DataStream{
			Index:    s.Index,
			Codec:    s.CodecName,
			CodecTag: codecTag(s.CodecTagString),
		})
	}
}

func videoFrom(s probeStream) VideoStream {
	v := VideoStream{
		Index:       s.Index,
		Codec:       Codec(s.CodecName),
		Profile:     string(s.Profile),
		CodecTag:    codecTag(s.CodecTagString),
		Width:       s.Width,
		Height:      s.Height,
		SAR:         s.SampleAspectRatio,
		DAR:         s.DisplayAspect,
		FrameRate:   frameRate(s.AvgFrameRate, s.RFrameRate),
		FieldOrder:  s.FieldOrder,
		BitDepth:    bitDepth(s.BitsPerRawSample, s.PixFmt),
		PixFmt:      s.PixFmt,
		Primaries:   colourValue(s.ColorPrimaries),
		Transfer:    colourValue(s.ColorTransfer),
		Matrix:      colourValue(s.ColorSpace),
		Range:       colourValue(s.ColorRange),
		BitRate:     streamBitRate(s.BitRate, s.Tags),
		Language:    language(s.Tags),
		Title:       tag(s.Tags, "title"),
		Disposition: dispositionOf(s.Disposition),
		Tags:        s.Tags,
	}
	v.Rotation = rotation(s.SideData, s.Tags)
	applySideData(&v, s.SideData)
	return v
}

func audioFrom(s probeStream) AudioStream {
	return AudioStream{
		Index:       s.Index,
		Codec:       s.CodecName,
		Profile:     string(s.Profile),
		Channels:    s.Channels,
		Layout:      s.ChannelLayout,
		SampleRate:  s.SampleRate.Int(),
		BitRate:     streamBitRate(s.BitRate, s.Tags),
		Language:    language(s.Tags),
		Title:       tag(s.Tags, "title"),
		Disposition: dispositionOf(s.Disposition),
	}
}

func dispositionOf(d map[string]int) Disposition {
	return Disposition{
		Default:         d["default"] != 0,
		Forced:          d["forced"] != 0,
		HearingImpaired: d["hearing_impaired"] != 0,
		VisualImpaired:  d["visual_impaired"] != 0,
		Comment:         d["comment"] != 0,
		Original:        d["original"] != 0,
		AttachedPic:     d["attached_pic"] != 0,
	}
}

// codecTag drops ffprobe's placeholder for "no tag" (MKV streams report
// "[0][0][0][0]").
func codecTag(s string) string {
	if s == "[0][0][0][0]" {
		return ""
	}
	return s
}

// colourValue treats ffprobe's "unknown" and "unspecified" as unset so they
// are never copied into encoder arguments or mistaken for a real value.
func colourValue(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "unknown", "unspecified":
		return ""
	}
	return s
}

// frameRate prefers the average frame rate; ffprobe writes "0/0" when it does
// not know it.
func frameRate(avg, base string) float64 {
	for _, s := range []string{avg, base} {
		if r, ok := parseRational(s); ok && r > 0 {
			return r
		}
	}
	return 0
}

// bitDepth prefers bits_per_raw_sample (often missing for HEVC and AV1) and
// otherwise reads the pixel format.
func bitDepth(raw flexString, pixFmt string) int {
	if n, ok := raw.Int64(); ok && n > 0 {
		return int(n)
	}
	return pixFmtDepth(pixFmt)
}

// pixFmtDepth infers bit depth from an ffmpeg pixel format name:
// yuv420p10le is 10, p010le is 10, nv12 and yuv410p are 8. It returns 0 when
// there is no pixel format, so an unreadable stream never passes as 8-bit.
func pixFmtDepth(pixFmt string) int {
	s := strings.ToLower(strings.TrimSpace(pixFmt))
	if s == "" {
		return 0
	}
	s = strings.TrimSuffix(strings.TrimSuffix(s, "le"), "be")
	// Semi-planar high bit depth formats: p010, p012, p016, p210, p410, ...
	if len(s) == 4 && s[0] == 'p' && allDigits(s[1:]) {
		return atoiOr(s[2:], 8)
	}
	// Planar formats end "p<depth>": yuv420p10, gbrp12, yuva444p16.
	if i := strings.LastIndexByte(s, 'p'); i >= 0 && i < len(s)-1 && allDigits(s[i+1:]) {
		return atoiOr(s[i+1:], 8)
	}
	// Grey formats: gray10, gray12.
	if rest, ok := strings.CutPrefix(s, "gray"); ok && rest != "" && allDigits(rest) {
		return atoiOr(rest, 8)
	}
	return 8
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func atoiOr(s string, fallback int) int {
	n, ok := flexString(s).Int64()
	if !ok || n <= 0 {
		return fallback
	}
	return int(n)
}

// streamBitRate is the stream's bit_rate, else the MKV statistics tag BPS
// (mkvmerge writes "BPS", sometimes with a language suffix such as
// "BPS-eng"). 0 means unknown.
func streamBitRate(br flexString, tags map[string]string) int64 {
	if n, ok := br.Int64(); ok && n > 0 {
		return n
	}
	for _, k := range sortedKeys(tags) {
		if strings.EqualFold(k, "BPS") {
			if n, ok := flexString(tags[k]).Int64(); ok && n > 0 {
				return n
			}
		}
	}
	for _, k := range sortedKeys(tags) {
		if len(k) > 3 && strings.EqualFold(k[:3], "BPS") {
			if n, ok := flexString(tags[k]).Int64(); ok && n > 0 {
				return n
			}
		}
	}
	return 0
}

// rotation comes from the Display Matrix side data (ffprobe 5 and later) or
// the older "rotate" tag. ffprobe reports degrees anticlockwise; the sign is
// kept as reported.
func rotation(side []sideData, tags map[string]string) int {
	for _, sd := range side {
		if sd.Type == "Display Matrix" {
			if n, ok := sd.Rotation.Int64(); ok {
				return int(n)
			}
		}
	}
	if n, ok := flexString(tag(tags, "rotate")).Int64(); ok {
		return int(n)
	}
	return 0
}

// language returns the stream's language tag, or "" when it is missing or
// "und". Stream order never implies a language.
func language(tags map[string]string) string {
	l := strings.TrimSpace(tag(tags, "language"))
	if strings.EqualFold(l, "und") {
		return ""
	}
	return l
}

// tag looks a key up case-insensitively (MKV tags are often upper case). An
// exact match wins; otherwise the first match in sorted key order, so the
// result never depends on map iteration order.
func tag(tags map[string]string, key string) string {
	if v, ok := tags[key]; ok {
		return v
	}
	for _, k := range sortedKeys(tags) {
		if strings.EqualFold(k, key) {
			return tags[k]
		}
	}
	return ""
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
