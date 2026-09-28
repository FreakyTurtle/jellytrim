package plan

import (
	"fmt"

	"github.com/freakyturtle/jellytrim/internal/media"
	"github.com/freakyturtle/jellytrim/internal/policy"
)

// MP4-safe stream codecs. Anything else in an MP4 source makes JellyTrim skip
// the file rather than drop the stream.
var (
	mp4Audio     = set("aac", "ac3", "eac3", "mp3", "opus", "flac", "alac")
	mp4Subtitles = set("mov_text")
	coverCodecs  = set("mjpeg", "png", "bmp", "gif")
)

func set(vs ...string) map[string]bool {
	m := make(map[string]bool, len(vs))
	for _, v := range vs {
		m[v] = true
	}
	return m
}

// safetyChecks returns every reason the file cannot be processed safely. An
// empty result means it is safe to consider.
func safetyChecks(f *media.File, facts FileFacts, a policy.Action, env Env) []Reason {
	var rs []Reason
	add := func(code, text string) { rs = append(rs, Reason{code, text}) }

	if !facts.Probed || f == nil {
		if facts.ProbeError != "" {
			add("probe_failed", "ffprobe could not read this file: "+facts.ProbeError)
		} else {
			add("not_probed", "JellyTrim has not inspected this file yet.")
		}
		return rs
	}
	rs = append(rs, fileChecks(facts)...)
	rs = append(rs, containerChecks(f)...)
	rs = append(rs, videoChecks(f)...)
	rs = append(rs, hdrChecks(f, a)...)
	_, reduce := outputHDR(f.HDR().Class)
	if why := reductionEncoderProblem(a, reduce); why != "" {
		add("hdr_encoder", why)
	}
	if f.Duration <= 0 {
		add("no_duration", "The file's duration is unknown, so the result could not be checked.")
	}
	if len(rs) == 0 {
		if _, ok, why := env.Encoders.Select(targetCodec(f, a), f.HDR().Class.IsHDR(), encoderFor(a, reduce)); !ok {
			add("no_encoder", why)
		}
	}
	return rs
}

func fileChecks(facts FileFacts) []Reason {
	var rs []Reason
	if facts.IsSymlink {
		rs = append(rs, Reason{"symlink", "The file is a symbolic link. JellyTrim only replaces real files."})
	}
	if facts.Nlink > 1 {
		rs = append(rs, Reason{"hardlink", fmt.Sprintf("The file has %d hard links (for example a seeding copy). Replacing it would use more space, not less.", facts.Nlink)})
	}
	if !facts.InRoots {
		rs = append(rs, Reason{"outside_roots", "The file is outside the folders JellyTrim is allowed to change (see path mappings)."})
	}
	return rs
}

func containerChecks(f *media.File) []Reason {
	var rs []Reason
	switch f.Container {
	case media.Matroska:
		for _, d := range f.Data {
			rs = append(rs, Reason{"data_stream", fmt.Sprintf("Stream %d is a data stream (%s) that cannot be carried over safely.", d.Index, orUnknown(d.Codec))})
		}
	case media.MP4:
		rs = append(rs, mp4Checks(f)...)
	default:
		rs = append(rs, Reason{"container", fmt.Sprintf("The container (%s) is not supported yet. JellyTrim handles MKV and MP4.", orUnknown(f.FormatName))})
	}
	for _, a := range f.Audio {
		if a.Codec == "" {
			rs = append(rs, Reason{"unknown_stream", fmt.Sprintf("Audio stream %d has an unknown codec.", a.Index)})
		}
	}
	for _, s := range f.Subtitles {
		if s.Codec == "" {
			rs = append(rs, Reason{"unknown_stream", fmt.Sprintf("Subtitle stream %d has an unknown format.", s.Index)})
		}
	}
	return rs
}

func mp4Checks(f *media.File) []Reason {
	var rs []Reason
	for _, a := range f.Audio {
		if a.Codec != "" && !mp4Audio[a.Codec] {
			rs = append(rs, Reason{"mp4_stream", fmt.Sprintf("Audio stream %d (%s) cannot be kept in MP4 reliably.", a.Index, a.Codec)})
		}
	}
	for _, s := range f.Subtitles {
		if s.Codec != "" && !mp4Subtitles[s.Codec] {
			rs = append(rs, Reason{"mp4_stream", fmt.Sprintf("Subtitle stream %d (%s) cannot be kept in MP4.", s.Index, s.Codec)})
		}
	}
	if len(f.Attachments) > 0 {
		rs = append(rs, Reason{"mp4_stream", "The file has attachments, which MP4 cannot hold."})
	}
	for _, d := range f.Data {
		rs = append(rs, Reason{"data_stream", fmt.Sprintf("Stream %d is a data stream (%s) that cannot be carried over safely.", d.Index, orUnknown(d.CodecTag))})
	}
	return rs
}

func videoChecks(f *media.File) []Reason {
	var rs []Reason
	switch n := f.RealVideoCount(); {
	case n == 0:
		return []Reason{{"no_video", "The file has no video stream."}}
	case n > 1:
		rs = append(rs, Reason{"multi_video", fmt.Sprintf("The file has %d video streams (for example alternative angles or a Dolby Vision layer). JellyTrim only handles one.", n)})
	}
	v, _ := f.MainVideo()
	if v.Codec == "" {
		rs = append(rs, Reason{"unknown_stream", "The video codec is unknown."})
	}
	if v.Width <= 0 || v.Height <= 0 {
		rs = append(rs, Reason{"no_resolution", "The video resolution is unknown."})
	}
	if v.Interlaced() {
		rs = append(rs, Reason{"interlaced", "The video is interlaced. Deinterlacing is not supported yet."})
	}
	if v.Rotation != 0 {
		rs = append(rs, Reason{"rotated", fmt.Sprintf("The video is rotated %d degrees. Rotated video is not supported yet.", v.Rotation)})
	}
	for _, c := range f.CoverArt() {
		if !coverCodecs[string(c.Codec)] {
			rs = append(rs, Reason{"cover_art", fmt.Sprintf("Stream %d is marked as cover art but is %s video.", c.Index, c.Codec.Label())})
		}
	}
	return rs
}

func hdrChecks(f *media.File, a policy.Action) []Reason {
	h := f.HDR()
	facts := joinFacts(h.Facts)
	switch h.Class {
	case media.DolbyVision:
		return []Reason{{"hdr_dolby_vision", "Dolby Vision without an HDR10 base layer cannot be converted safely. " + facts}}
	case media.Unclear:
		return []Reason{{"hdr_unclear", "The HDR information is unclear, so JellyTrim will not risk damaging the picture. " + facts}}
	case media.HDR10Plus, media.DolbyVisionHDR10:
		if !a.AllowHDRReduction {
			return []Reason{{"hdr_reduction", h.Class.Label() + " would be reduced to HDR10. Allow this in the policy to convert it. " + facts}}
		}
	}
	return nil
}

func joinFacts(fs []string) string {
	if len(fs) == 0 {
		return ""
	}
	out := "("
	for i, f := range fs {
		if i > 0 {
			out += "; "
		}
		out += f
	}
	return out + ")"
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// targetCodec is the codec the output will use.
func targetCodec(f *media.File, a policy.Action) media.Codec {
	if c := a.TargetCodec(); c != "" {
		return c
	}
	v, _ := f.MainVideo()
	return v.Codec
}
