package encoder

import (
	"path"
	"sort"
	"testing"

	"github.com/freakyturtle/jellytrim/internal/media"
	"github.com/freakyturtle/jellytrim/internal/plan"
	"github.com/freakyturtle/jellytrim/internal/policy"
	"github.com/freakyturtle/jellytrim/internal/testutil"
)

// loadFixture parses a fixture's committed ffprobe JSON.
func loadFixture(t *testing.T, name string) *media.File {
	t.Helper()
	probe, frames := testutil.ProbeJSON(t, name)
	f, err := media.Parse(probe, frames, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", name, err)
	}
	return f
}

// planFor builds the plan internal/plan would make for an HEVC encode of f
// at w x h (0 keeps the source size). It follows plan.decideTarget so the
// golden tests do not depend on the worth-it checks.
func planFor(t *testing.T, f *media.File, w, h int, quality string) plan.Plan {
	t.Helper()
	v, ok := f.MainVideo()
	if !ok {
		t.Fatal("fixture has no main video")
	}
	p := plan.Plan{
		Codec: media.CodecHEVC, Encoder: NameX265, Quality: quality,
		Width: v.Width, Height: v.Height, BitDepth: 10, Container: f.Container,
		VideoIndex: v.Index, SourceSize: f.Size,
		Primaries: v.Primaries, Transfer: v.Transfer, Matrix: v.Matrix,
		Mastering: v.Mastering, ContentLight: v.ContentLight,
	}
	if w > 0 {
		p.Width, p.Height, p.Scale = w, h, true
	}
	switch class := f.HDR().Class; class {
	case media.HDR10Plus, media.DolbyVisionHDR10:
		p.HDR, p.ReduceHDR = media.HDR10, true
	default:
		p.HDR = class
	}
	for i := range copyable(f, v.Index) {
		p.CopyIndices = append(p.CopyIndices, i)
	}
	sort.Ints(p.CopyIndices)
	return p
}

// jobFor is a job writing the partial file next to the source, as the
// pipeline does.
func jobFor(f *media.File, p plan.Plan) Job {
	dir, name := path.Split(f.Path)
	return Job{Plan: p, Source: f, Input: f.Path, Output: dir + "." + name + ".jellytrim-42.partial", JobID: 42}
}

// hdrX265 is x265 as detection finds it with a current ffmpeg.
func hdrX265() Backend {
	return X265{Cap: Capability{Backend: NameX265, Available: true, HDRPassthrough: true, DolbyVisionOption: true}}
}

// qsvWith is QSV with the given hardware decoders and HDR passthrough.
func qsvWith(hdr bool, decode ...string) Backend {
	return QSV{Cap: Capability{Backend: NameQSV, Available: true, HDRPassthrough: hdr, HWDecode: decode}}
}

var tiers = []string{policy.QualityMaximum, policy.QualityHigh, policy.QualityBalanced, policy.QualitySpaceSaver}
