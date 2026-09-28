package plan

import (
	"fmt"
	"math"
	"sort"

	"github.com/freakyturtle/jellytrim/internal/media"
	"github.com/freakyturtle/jellytrim/internal/policy"
	"github.com/freakyturtle/jellytrim/internal/units"
)

// bitsPerPixel is the starting estimate for HEVC 10-bit film content per
// quality tier. See docs/TRANSCODING.md ("Size estimates").
var bitsPerPixel = map[string]float64{
	policy.QualityMaximum:    0.080,
	policy.QualityHigh:       0.055,
	policy.QualityBalanced:   0.038,
	policy.QualitySpaceSaver: 0.026,
}

// codecFactor scales the HEVC estimate for other codecs.
func codecFactor(c media.Codec) float64 {
	switch c {
	case media.CodecH264:
		return 1.6
	case media.CodecAV1:
		return 0.8
	}
	return 1.0
}

// inefficientFactor: a same-codec file whose video bitrate is above this
// multiple of the upper estimate is worth re-encoding.
const inefficientFactor = 2.0

func decideTarget(d Decision, f *media.File, facts FileFacts, a policy.Action, env Env) Decision {
	v, _ := f.MainVideo()
	quality := a.Quality
	if quality == "" {
		quality = env.DefaultQuality
	}
	if _, ok := bitsPerPixel[quality]; !ok {
		quality = policy.QualityHigh
	}
	codec := targetCodec(f, a)
	width, height, scale := targetSize(v, a.MaxRes())

	p := &Plan{
		Codec: codec, Quality: quality, Width: width, Height: height, Scale: scale,
		BitDepth: bitDepth(v, codec, f.HDR().Class, env.MatchBitDepth), Container: f.Container,
		VideoIndex: v.Index, CopyIndices: copyIndices(f, v.Index),
		SourceSize: f.Size, SourceLabel: sourceLabel(v),
		Primaries: v.Primaries, Transfer: v.Transfer, Matrix: v.Matrix,
		Mastering: v.Mastering, ContentLight: v.ContentLight,
	}
	p.HDR, p.ReduceHDR = outputHDR(f.HDR().Class)
	p.TargetLabel = media.ClassOf(width, height).Label() + " " + codec.Label()
	name, _, _ := env.Encoders.Select(codec, p.HDR.IsHDR(), encoderFor(a, p.ReduceHDR))
	p.Encoder = name
	p.EstMin, p.EstMax = estimate(f, v, p)

	if facts.Optimised && !scale {
		return optimal(d, "JellyTrim already optimised this file.")
	}
	if !scale && a.TargetCodec() == "" {
		// "Keep existing codec" only ever asks for a smaller resolution.
		return optimal(d, fmt.Sprintf("Already at or below %s; the policy keeps the codec.", resLabel(a.MaxRes(), v)))
	}
	srcBps, bpsKnown := f.VideoBitrate()
	if !scale && codec.Efficiency() <= v.Codec.Efficiency() {
		upper := videoBps(p, v.FrameRate, bitsPerPixel[quality]*codecFactor(codec)*1.3)
		if !bpsKnown || float64(srcBps) <= inefficientFactor*upper {
			if codec == v.Codec {
				return optimal(d, fmt.Sprintf("Already %s at %s.", v.Codec.Label(), v.Resolution().Label()))
			}
			return optimal(d, fmt.Sprintf("Already in %s, which is at least as efficient as %s.", v.Codec.Label(), codec.Label()))
		}
		d.Reasons = append(d.Reasons, Reason{"inefficient", fmt.Sprintf("%s at %s is far above what %s quality needs.", v.Codec.Label(), units.Bitrate(srcBps), policy.QualityLabel(quality))})
	}

	low, high := p.EstSaving()
	mid := (low + high) / 2
	if f.Size <= 0 || float64(mid) < float64(f.Size)*float64(env.MinSavingPercent)/100 {
		pct := 0
		if f.Size > 0 {
			pct = int(math.Round(float64(mid) / float64(f.Size) * 100))
		}
		return optimal(d, fmt.Sprintf("The estimated saving (about %d%%) is below the %d%% minimum.", pct, env.MinSavingPercent))
	}

	d.Outcome = Optimise
	d.Plan = p
	d.Summary = fmt.Sprintf("%s → %s, saving about %s to %s (estimate).", p.SourceLabel, p.TargetLabel, units.Bytes(low), units.Bytes(high))
	if p.ReduceHDR {
		d.Reasons = append(d.Reasons, Reason{"hdr_reduced", f.HDR().Class.Label() + " will become HDR10, as the policy allows."})
	}
	return d
}

func optimal(d Decision, why string) Decision {
	d.Outcome = AlreadyOptimal
	d.Summary = why
	d.Reasons = append(d.Reasons, Reason{"optimal", why})
	return d
}

// targetSize applies the resolution cap, never upscaling, keeping the aspect
// ratio and even dimensions.
func targetSize(v media.VideoStream, limit media.Resolution) (w, h int, scale bool) {
	if limit == 0 || v.Resolution() <= limit {
		return v.Width, v.Height, false
	}
	f := math.Min(float64(limit.Width())/float64(v.Width), float64(limit)/float64(v.Height))
	if f >= 1 {
		return v.Width, v.Height, false
	}
	w = even(float64(v.Width) * f)
	h = even(float64(v.Height) * f)
	return w, h, true
}

func even(x float64) int {
	n := int(math.Round(x))
	if n%2 == 1 {
		n--
	}
	return max(n, 2)
}

func bitDepth(v media.VideoStream, codec media.Codec, class media.HDRClass, match bool) int {
	switch {
	case class.IsHDR():
		return 10
	case codec == media.CodecH264:
		return 8
	case match && v.BitDepth > 0 && v.BitDepth <= 8:
		return 8
	}
	return 10
}

// outputHDR is the class the output will have, and whether dynamic metadata
// or a Dolby Vision layer is being dropped.
func outputHDR(c media.HDRClass) (media.HDRClass, bool) {
	switch c {
	case media.HDR10Plus, media.DolbyVisionHDR10:
		return media.HDR10, true
	}
	return c, false
}

// copyIndices lists every stream copied unchanged: all audio, subtitles,
// attachments and cover art, in source order.
func copyIndices(f *media.File, main int) []int {
	var idx []int
	for _, v := range f.Video {
		if v.Index != main && v.Disposition.AttachedPic {
			idx = append(idx, v.Index)
		}
	}
	for _, a := range f.Audio {
		idx = append(idx, a.Index)
	}
	for _, s := range f.Subtitles {
		idx = append(idx, s.Index)
	}
	for _, a := range f.Attachments {
		idx = append(idx, a.Index)
	}
	sort.Ints(idx)
	return idx
}

func videoBps(p *Plan, fps, bpp float64) float64 {
	if fps <= 0 || fps > 120 {
		fps = 24
	}
	return bpp * float64(p.Width*p.Height) * fps
}

// estimate returns the estimated output size range in bytes.
func estimate(f *media.File, v media.VideoStream, p *Plan) (lo, hi int64) {
	secs := f.Duration.Seconds()
	video := videoBps(p, v.FrameRate, bitsPerPixel[p.Quality]*codecFactor(p.Codec)) * secs / 8
	other := otherBytes(f, secs)
	lo = int64(video*0.7 + other)
	hi = int64(video*1.3 + other)
	// Never estimate more than the source video costs now.
	if srcBps, ok := f.VideoBitrate(); ok {
		srcVideo := float64(srcBps) * secs / 8
		if video > srcVideo {
			lo = int64(math.Min(float64(lo), srcVideo*0.7+other))
			hi = int64(math.Min(float64(hi), srcVideo+other))
		}
	}
	if f.Size > 0 && hi > f.Size {
		hi = f.Size
	}
	if lo > hi {
		lo = hi
	}
	return lo, hi
}

// otherBytes estimates everything that is copied: audio, subtitles, attachments.
func otherBytes(f *media.File, secs float64) float64 {
	if srcBps, ok := f.VideoBitrate(); ok && f.Size > 0 {
		rest := float64(f.Size) - float64(srcBps)*secs/8
		if rest > 0 {
			return rest
		}
	}
	var audio int64
	for _, a := range f.Audio {
		audio += a.BitRate
	}
	return float64(audio) * secs / 8
}

func resLabel(limit media.Resolution, v media.VideoStream) string {
	if limit == 0 {
		return v.Resolution().Label()
	}
	return limit.Label()
}

// softwareEncoder is the only backend that can reduce Dolby Vision or
// HDR10+ to HDR10: it needs x265's -dolbyvision option, which hardware
// encoders lack.
const softwareEncoder = "x265"

// encoderFor picks the encoder to ask for. When the policy leaves the choice
// to JellyTrim and the HDR is being reduced, that is the software encoder.
func encoderFor(a policy.Action, reduceHDR bool) string {
	if reduceHDR && autoEncoder(a.Encoder) {
		return softwareEncoder
	}
	return a.Encoder
}

// reductionEncoderProblem explains why a reduction cannot run when the
// policy names an encoder other than the software one, or returns "".
func reductionEncoderProblem(a policy.Action, reduceHDR bool) string {
	if !reduceHDR || !a.AllowHDRReduction || autoEncoder(a.Encoder) || a.Encoder == softwareEncoder {
		return ""
	}
	return "Reducing Dolby Vision or HDR10+ to HDR10 needs the software encoder; this policy asks for " + a.Encoder + "."
}

func autoEncoder(name string) bool { return name == "" || name == "auto" }
