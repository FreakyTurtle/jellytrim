package encoder

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"

	"github.com/freakyturtle/jellytrim/internal/media"
)

// DefaultQSVDevice is the usual render node of the first Intel GPU.
const DefaultQSVDevice = "/dev/dri/renderD128"

// deviceRe limits the device to a DRM render node path; the value goes
// inside an ffmpeg device string where ',' and ':' are separators.
var deviceRe = regexp.MustCompile(`^/dev/dri/[A-Za-z0-9_]+$`)

// QSV is the Intel Quick Sync HEVC backend (hevc_qsv through VA-API on
// Linux). It has golden argument tests but has not yet been tried on real
// Intel hardware by the maintainers.
type QSV struct {
	Device string // render node; "" means DefaultQSVDevice
	Cap    Capability
}

var _ Backend = QSV{}

// Name implements Backend.
func (QSV) Name() string { return NameQSV }

// Label implements Backend.
func (QSV) Label() string { return "Intel Quick Sync (HEVC)" }

// Codec implements Backend.
func (QSV) Codec() media.Codec { return media.CodecHEVC }

// Hardware implements Backend.
func (QSV) Hardware() bool { return true }

// FFmpegEncoder implements Backend.
func (QSV) FFmpegEncoder() string { return "hevc_qsv" }

// QualityValue is the ICQ global_quality for a tier.
func (QSV) QualityValue(tier string, o Overrides) int { return qualityValue(NameQSV, tier, o) }

// Capability implements Backend.
func (q QSV) Capability() Capability { return q.Cap }

// WithCapability implements Backend.
func (q QSV) WithCapability(c Capability) Backend { q.Cap = c; return q }

func (q QSV) device() string {
	if q.Device == "" {
		return DefaultQSVDevice
	}
	return q.Device
}

// Check implements Backend.
func (q QSV) Check(Job) error {
	if !deviceRe.MatchString(q.device()) {
		return fmt.Errorf("QSV device %q is not a /dev/dri render node", q.device())
	}
	return nil
}

// decodeKey names a source for Capability.HWDecode, for example "hevc/10".
func decodeKey(codec media.Codec, depth int) string {
	return string(codec) + "/" + strconv.Itoa(depth)
}

// hwDecode reports whether the GPU proved it decodes this source.
func (q QSV) hwDecode(j Job) bool {
	if j.Source == nil {
		return false
	}
	v, ok := videoByIndex(j.Source, j.Plan.VideoIndex)
	return ok && slices.Contains(q.Cap.HWDecode, decodeKey(v.Codec, v.BitDepth))
}

// InputArgs implements Backend. The device is always initialised (the
// software-decode path uploads frames to it); hardware decoding is used
// only for sources the GPU decoded during detection.
func (q QSV) InputArgs(j Job) []string {
	args := []string{
		"-init_hw_device", "vaapi=va:" + q.device() + ",driver=iHD",
		"-init_hw_device", "qsv=qs@va",
		"-filter_hw_device", "qs",
	}
	if q.hwDecode(j) {
		args = append(args, "-hwaccel", "qsv", "-hwaccel_output_format", "qsv")
	}
	return args
}

// FilterChain implements Backend: [upload,] vpp_qsv (scale and format),
// [keep SAR,] colour tags.
func (q QSV) FilterChain(j Job) (string, error) {
	col, err := colourOf(j.Plan)
	if err != nil {
		return "", err
	}
	swFormat, hwFormat := "p010le", "p010"
	if j.Plan.BitDepth == 8 {
		swFormat, hwFormat = "nv12", "nv12"
	}
	var upload, sar string
	if !q.hwDecode(j) {
		upload = "format=" + swFormat + ",hwupload=extra_hw_frames=64"
	}
	vpp := "vpp_qsv=format=" + hwFormat
	if j.Plan.Scale {
		vpp = fmt.Sprintf("vpp_qsv=w=%d:h=%d:format=%s", j.Plan.Width, j.Plan.Height, hwFormat)
		sar = sarFilter(mainSAR(j))
	}
	return joinFilters(upload, vpp, sar, col.setparams()), nil
}

// EncoderArgs implements Backend. ICQ mode: global_quality without a
// bitrate. HDR10 static metadata is carried from the decoded frames' side
// data, which detection proves before HDR jobs are allowed.
func (q QSV) EncoderArgs(j Job) ([]string, error) {
	return []string{
		"-c:v:0", "hevc_qsv",
		"-preset:v:0", "slow",
		"-global_quality:v:0", strconv.Itoa(q.QualityValue(j.Plan.Quality, j.Overrides)),
		"-profile:v:0", profile(j.Plan.BitDepth),
	}, nil
}
