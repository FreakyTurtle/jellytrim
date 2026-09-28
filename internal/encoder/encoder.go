// Package encoder turns an encode plan into ffmpeg arguments for one of
// JellyTrim's encoder backends (software x265, Intel Quick Sync), proves
// each backend works on this machine with real test encodes, and chooses a
// backend for each job. See docs/TRANSCODING.md section 4.
package encoder

import (
	"fmt"
	"time"

	"github.com/freakyturtle/jellytrim/internal/media"
	"github.com/freakyturtle/jellytrim/internal/plan"
	"github.com/freakyturtle/jellytrim/internal/policy"
)

// Backend names, as stored in settings and policies.
const (
	NameX265 = "x265"
	NameQSV  = "qsv-hevc"
)

// Backend is one way of producing a codec.
type Backend interface {
	// Name is the stable identifier, for example "x265".
	Name() string
	// Label is the name shown to users, for example "Software (x265)".
	Label() string
	Codec() media.Codec
	Hardware() bool
	// FFmpegEncoder is the ffmpeg encoder it uses, for example "libx265".
	FFmpegEncoder() string
	// QualityValue is the native quality number for a tier, with any
	// override applied, or 0 for an unknown tier.
	QualityValue(tier string, o Overrides) int
	// Capability is the result of the last detection; the zero value
	// before detection.
	Capability() Capability
	// WithCapability returns a copy of the backend that knows c.
	WithCapability(c Capability) Backend

	// Check refuses jobs this backend cannot do safely.
	Check(j Job) error
	// InputArgs are the options that go before -i.
	InputArgs(j Job) []string
	// FilterChain is the -filter:v:0 value, or "" for none.
	FilterChain(j Job) (string, error)
	// EncoderArgs select and configure the encoder for output stream v:0.
	EncoderArgs(j Job) ([]string, error)
}

// Job is one encode: the plan, the probed source and where to write.
type Job struct {
	Plan      plan.Plan
	Source    *media.File
	Input     string // source path
	Output    string // partial file path
	JobID     int64
	Overrides Overrides
}

// Overrides are the Advanced settings that change encoder options.
type Overrides struct {
	// Quality holds per-backend, per-tier numbers, for example
	// {"x265": {"high": 20}}.
	Quality map[string]map[string]int `json:"quality,omitempty"`
	// Preset is the x265 preset: medium, slow (default) or slower.
	Preset string `json:"preset,omitempty"`
	// Threads limits x265's thread pool; 0 lets x265 decide.
	Threads int `json:"threads,omitempty"`
}

// x265Presets are the presets offered in Advanced settings.
var x265Presets = map[string]bool{"medium": true, "slow": true, "slower": true}

// Limits for overrides.
const (
	maxQuality = 51
	maxThreads = 256
)

// Validate reports the first invalid override.
func (o Overrides) Validate() error {
	if o.Preset != "" && !x265Presets[o.Preset] {
		return fmt.Errorf("x265 preset %q is not one of medium, slow or slower", o.Preset)
	}
	if o.Threads < 0 || o.Threads > maxThreads {
		return fmt.Errorf("threads must be between 0 and %d", maxThreads)
	}
	for backend, tiers := range o.Quality {
		for tier, v := range tiers {
			if _, ok := defaultQuality(backend, tier); !ok {
				return fmt.Errorf("unknown quality override %s/%s", backend, tier)
			}
			if v < 1 || v > maxQuality {
				return fmt.Errorf("quality override %s/%s must be between 1 and %d", backend, tier, maxQuality)
			}
		}
	}
	return nil
}

// Quality maps (docs/TRANSCODING.md, "Quality maps"). The numbers are not
// equivalent across encoders: x265 CRF and QSV ICQ scales differ, and the
// QSV values sit one step higher to give similar sizes.
var qualityMaps = map[string]map[string]int{
	NameX265: {
		policy.QualityMaximum:    18,
		policy.QualityHigh:       21,
		policy.QualityBalanced:   24,
		policy.QualitySpaceSaver: 27,
	},
	NameQSV: {
		policy.QualityMaximum:    19,
		policy.QualityHigh:       22,
		policy.QualityBalanced:   25,
		policy.QualitySpaceSaver: 28,
	},
}

func defaultQuality(backend, tier string) (int, bool) {
	v, ok := qualityMaps[backend][tier]
	return v, ok
}

// qualityValue applies an in-range override to the default for a tier.
func qualityValue(backend, tier string, o Overrides) int {
	def, ok := defaultQuality(backend, tier)
	if !ok {
		return 0
	}
	if v, ok := o.Quality[backend][tier]; ok && v >= 1 && v <= maxQuality {
		return v
	}
	return def
}

// Capability is what detection proved about a backend on this machine.
type Capability struct {
	Backend           string      `json:"backend"`
	Label             string      `json:"label"`
	Codec             media.Codec `json:"codec"`
	Hardware          bool        `json:"hardware"`
	Available         bool        `json:"available"`
	HDRPassthrough    bool        `json:"hdr_passthrough"`
	DolbyVisionOption bool        `json:"dolby_vision_option"`
	// HWDecode lists the sources the GPU decoded in the test, as
	// "codec/bit depth" (for example "hevc/10"). Other sources are decoded
	// in software and uploaded.
	HWDecode      []string  `json:"hw_decode,omitempty"`
	Device        string    `json:"device,omitempty"`
	Detail        string    `json:"detail,omitempty"`
	Error         string    `json:"error,omitempty"`
	FFmpegVersion string    `json:"ffmpeg_version,omitempty"`
	TestedAt      time.Time `json:"tested_at"`
}

// capabilityFor starts a Capability with the backend's identity.
func capabilityFor(b Backend) Capability {
	return Capability{Backend: b.Name(), Label: b.Label(), Codec: b.Codec(), Hardware: b.Hardware()}
}
