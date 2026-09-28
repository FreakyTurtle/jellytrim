package encoder

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/freakyturtle/jellytrim/internal/media"
)

// X265 is the software HEVC backend (libx265).
type X265 struct {
	Cap Capability
}

var _ Backend = X265{}

// Name implements Backend.
func (X265) Name() string { return NameX265 }

// Label implements Backend.
func (X265) Label() string { return "Software (x265)" }

// Codec implements Backend.
func (X265) Codec() media.Codec { return media.CodecHEVC }

// Hardware implements Backend.
func (X265) Hardware() bool { return false }

// FFmpegEncoder implements Backend.
func (X265) FFmpegEncoder() string { return "libx265" }

// QualityValue is the CRF for a tier.
func (X265) QualityValue(tier string, o Overrides) int { return qualityValue(NameX265, tier, o) }

// Capability implements Backend.
func (x X265) Capability() Capability { return x.Cap }

// WithCapability implements Backend.
func (x X265) WithCapability(c Capability) Backend { x.Cap = c; return x }

// Check implements Backend. x265 has no job-specific limits beyond the
// common checks.
func (X265) Check(Job) error { return nil }

// InputArgs implements Backend: software decoding needs no input options.
func (X265) InputArgs(Job) []string { return nil }

// dynamicMetadataFilters delete Dolby Vision RPUs and HDR10+ metadata from
// decoded frames when the policy allows reducing to HDR10, so no encoder
// can forward them. -dolbyvision 0 covers the encoder side as well.
const dynamicMetadataFilters = "sidedata=mode=delete:type=DOVI_RPU_BUFFER," +
	"sidedata=mode=delete:type=DOVI_METADATA," +
	"sidedata=mode=delete:type=DYNAMIC_HDR_PLUS"

// FilterChain implements Backend: [drop dynamic metadata,] [scale, keep SAR,]
// pixel format, colour tags.
func (X265) FilterChain(j Job) (string, error) {
	col, err := colourOf(j.Plan)
	if err != nil {
		return "", err
	}
	var reduce, scale, sar string
	if j.Plan.ReduceHDR {
		reduce = dynamicMetadataFilters
	}
	if j.Plan.Scale {
		scale = fmt.Sprintf("scale=%d:%d:flags=lanczos", j.Plan.Width, j.Plan.Height)
		sar = sarFilter(mainSAR(j))
	}
	format := "format=yuv420p10le"
	if j.Plan.BitDepth == 8 {
		format = "format=yuv420p"
	}
	return joinFilters(reduce, scale, sar, format, col.setparams()), nil
}

// EncoderArgs implements Backend.
func (x X265) EncoderArgs(j Job) ([]string, error) {
	params, err := x265Params(j)
	if err != nil {
		return nil, err
	}
	preset := j.Overrides.Preset
	if preset == "" {
		preset = "slow"
	}
	args := []string{
		"-c:v:0", "libx265",
		"-preset:v:0", preset,
		"-crf:v:0", strconv.Itoa(x.QualityValue(j.Plan.Quality, j.Overrides)),
		"-profile:v:0", profile(j.Plan.BitDepth),
		"-x265-params:v:0", params,
	}
	if j.Plan.ReduceHDR {
		args = append(args, "-dolbyvision:v:0", "0")
	}
	return args, nil
}

func profile(depth int) string {
	if depth == 8 {
		return "main"
	}
	return "main10"
}

// x265Params builds the -x265-params value. HDR10 signals the static
// metadata in every keyframe's headers; HLG only needs the colour
// description.
func x265Params(j Job) (string, error) {
	parts := []string{"log-level=error"}
	if j.Overrides.Threads > 0 {
		parts = append(parts, "pools="+strconv.Itoa(j.Overrides.Threads))
	}
	col, err := colourOf(j.Plan)
	if err != nil {
		return "", err
	}
	switch j.Plan.HDR {
	case media.HDR10:
		parts = append(parts, "hdr10=1", "hdr10-opt=1", "repeat-headers=1")
		parts = append(parts, "colorprim="+col.primaries, "transfer="+col.transfer, "colormatrix="+col.matrix)
		if m := j.Plan.Mastering; m != nil {
			parts = append(parts, "master-display="+masterDisplay(*m))
		}
		if cl := j.Plan.ContentLight; cl != nil {
			parts = append(parts, fmt.Sprintf("max-cll=%d,%d", cl.MaxCLL, cl.MaxFALL))
		}
	case media.HLG:
		parts = append(parts, "colorprim="+col.primaries, "transfer="+col.transfer, "colormatrix="+col.matrix)
	}
	return strings.Join(parts, ":"), nil
}

// masterDisplay formats SMPTE ST 2086 metadata the way x265 wants it; the
// media model already holds it in x265 units.
func masterDisplay(m media.MasteringDisplay) string {
	return fmt.Sprintf("G(%d,%d)B(%d,%d)R(%d,%d)WP(%d,%d)L(%d,%d)",
		m.GreenX, m.GreenY, m.BlueX, m.BlueY, m.RedX, m.RedY, m.WhiteX, m.WhiteY, m.MaxLuminance, m.MinLuminance)
}

func mainSAR(j Job) string {
	if j.Source == nil {
		return ""
	}
	v, _ := videoByIndex(j.Source, j.Plan.VideoIndex)
	return v.SAR
}
