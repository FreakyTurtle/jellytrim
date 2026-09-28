package encoder

import (
	"errors"
	"fmt"

	"github.com/freakyturtle/jellytrim/internal/media"
)

// validate checks a job before any argument is built. Every check guards a
// way the encode could damage or silently change the user's media.
func validate(b Backend, j Job, m buildMode) error {
	if j.Source == nil {
		return errors.New("the job has no probed source")
	}
	if m.input == nil && j.Input == "" {
		return errors.New("the job has no input path")
	}
	if j.Output == "" || j.Output == j.Input {
		return errors.New("the output path is empty or the same as the input")
	}
	if err := validatePlan(b, j); err != nil {
		return err
	}
	if err := validateHDR(b, j, m); err != nil {
		return err
	}
	if m.input == nil {
		if err := validateStreams(j); err != nil {
			return err
		}
	}
	if b.QualityValue(j.Plan.Quality, j.Overrides) == 0 {
		return fmt.Errorf("unknown quality tier %q", j.Plan.Quality)
	}
	if err := j.Overrides.Validate(); err != nil {
		return err
	}
	return b.Check(j)
}

func validatePlan(b Backend, j Job) error {
	p := j.Plan
	if p.Codec != b.Codec() {
		return fmt.Errorf("%s produces %s, but the plan asks for %s", b.Label(), b.Codec().Label(), p.Codec.Label())
	}
	if p.Container != media.Matroska && p.Container != media.MP4 {
		return fmt.Errorf("output container %q is not Matroska or MP4", p.Container)
	}
	if p.Width <= 0 || p.Height <= 0 || p.Width%2 != 0 || p.Height%2 != 0 {
		return fmt.Errorf("output size %dx%d is not a positive even size", p.Width, p.Height)
	}
	if p.BitDepth != 8 && p.BitDepth != 10 {
		return fmt.Errorf("output bit depth %d is not 8 or 10", p.BitDepth)
	}
	return nil
}

// validateHDR allows SDR, HDR10 and HLG output only. HDR10+ and Dolby
// Vision sources reach here as HDR10 with ReduceHDR set.
func validateHDR(b Backend, j Job, m buildMode) error {
	p := j.Plan
	switch p.HDR {
	case media.SDR:
		return nil
	case media.HDR10, media.HLG:
	default:
		return fmt.Errorf("output dynamic range %q is not SDR, HDR10 or HLG", p.HDR)
	}
	if p.BitDepth != 10 {
		return fmt.Errorf("%s output must be 10-bit", p.HDR.Label())
	}
	c := b.Capability()
	if !m.detecting && !c.HDRPassthrough {
		return fmt.Errorf("%s has not proved that it keeps HDR metadata", b.Label())
	}
	if p.ReduceHDR && (p.HDR != media.HDR10 || (!m.detecting && !c.DolbyVisionOption)) {
		return fmt.Errorf("%s cannot guarantee the Dolby Vision or HDR10+ layer is dropped", b.Label())
	}
	return nil
}

// validateStreams checks the plan against the source: the main video
// exists, is not scaled up, and every other stream is copied. Nothing may be
// dropped silently.
func validateStreams(j Job) error {
	main, ok := videoByIndex(j.Source, j.Plan.VideoIndex)
	if !ok || main.Disposition.AttachedPic {
		return fmt.Errorf("stream %d is not the source's main video", j.Plan.VideoIndex)
	}
	p := j.Plan
	if !p.Scale && (p.Width != main.Width || p.Height != main.Height) {
		return fmt.Errorf("output size %dx%d differs from the source %dx%d without scaling", p.Width, p.Height, main.Width, main.Height)
	}
	if p.Scale && (p.Width > main.Width || p.Height > main.Height) {
		return fmt.Errorf("output size %dx%d would upscale the source %dx%d", p.Width, p.Height, main.Width, main.Height)
	}
	if len(j.Source.Data) > 0 {
		return fmt.Errorf("stream %d is a data stream, which cannot be carried over", j.Source.Data[0].Index)
	}
	want := copyable(j.Source, p.VideoIndex)
	seen := map[int]bool{}
	for _, i := range p.CopyIndices {
		if !want[i] || seen[i] {
			return fmt.Errorf("copy list names stream %d, which is not a stream to copy", i)
		}
		seen[i] = true
	}
	for i := range want {
		if !seen[i] {
			return fmt.Errorf("stream %d would be dropped", i)
		}
	}
	return nil
}

func videoByIndex(f *media.File, index int) (media.VideoStream, bool) {
	for _, v := range f.Video {
		if v.Index == index {
			return v, true
		}
	}
	return media.VideoStream{}, false
}

// copyable is every stream index that must be copied unchanged.
func copyable(f *media.File, main int) map[int]bool {
	want := map[int]bool{}
	for _, v := range f.Video {
		if v.Index != main {
			want[v.Index] = true
		}
	}
	for _, a := range f.Audio {
		want[a.Index] = true
	}
	for _, s := range f.Subtitles {
		want[s.Index] = true
	}
	for _, a := range f.Attachments {
		want[a.Index] = true
	}
	return want
}
