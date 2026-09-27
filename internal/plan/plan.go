// Package plan decides what JellyTrim should do with one item: optimise it
// (with an encode plan and a size estimate), leave it alone, or skip it with
// reasons. It is pure: the caller supplies the probe, the file facts and the
// policy result. See docs/TRANSCODING.md section 3.
package plan

import (
	"github.com/freakyturtle/jellytrim/internal/media"
	"github.com/freakyturtle/jellytrim/internal/policy"
)

// Outcome is the decision for an item.
type Outcome string

// Outcomes.
const (
	Optimise       Outcome = "optimise"
	AlreadyOptimal Outcome = "optimal"
	Protected      Outcome = "protected"
	Skipped        Outcome = "skipped"
	NoPolicy       Outcome = "no_policy"
)

// Label is the user-facing name.
func (o Outcome) Label() string {
	switch o {
	case Optimise:
		return "Needs optimisation"
	case AlreadyOptimal:
		return "Already optimal"
	case Protected:
		return "Protected"
	case Skipped:
		return "Skipped"
	}
	return "No policy"
}

// Reason is one fact behind a decision, with a stable code for filtering and
// a plain-English sentence for people.
type Reason struct {
	Code string `json:"code"`
	Text string `json:"text"`
}

// FileFacts are filesystem facts the caller gathered about the source.
type FileFacts struct {
	Probed     bool
	ProbeError string
	IsSymlink  bool
	Nlink      int
	InRoots    bool
	Optimised  bool // JellyTrim produced this exact file (see the optimised table)
}

// Encoders reports which encoder would handle a target. Implemented by
// internal/encoder; tests use a fake.
type Encoders interface {
	// Select returns the backend name for the codec. hdr asks for one that
	// is proven to preserve HDR metadata. requested is "auto" or a backend.
	Select(codec media.Codec, hdr bool, requested string) (name string, ok bool, why string)
}

// Env holds settings that affect decisions.
type Env struct {
	MinSavingPercent int
	MatchBitDepth    bool
	DefaultQuality   string
	Encoders         Encoders
}

// Plan is everything the pipeline needs to encode one file.
type Plan struct {
	Codec        media.Codec             `json:"codec"`
	Encoder      string                  `json:"encoder"`
	Quality      string                  `json:"quality"`
	Width        int                     `json:"width"`
	Height       int                     `json:"height"`
	Scale        bool                    `json:"scale"`
	BitDepth     int                     `json:"bit_depth"`
	HDR          media.HDRClass          `json:"hdr"` // the class the output will have
	ReduceHDR    bool                    `json:"reduce_hdr,omitempty"`
	Container    media.Container         `json:"container"`
	VideoIndex   int                     `json:"video_index"`
	CopyIndices  []int                   `json:"copy_indices"` // audio, subtitles, attachments and cover art, in source order
	SourceSize   int64                   `json:"source_size"`
	EstMin       int64                   `json:"est_min"`
	EstMax       int64                   `json:"est_max"`
	SourceLabel  string                  `json:"source_label"` // "2160p H.264"
	TargetLabel  string                  `json:"target_label"` // "1080p HEVC"
	Mastering    *media.MasteringDisplay `json:"mastering,omitempty"`
	ContentLight *media.ContentLight     `json:"content_light,omitempty"`
	Primaries    string                  `json:"primaries,omitempty"`
	Transfer     string                  `json:"transfer,omitempty"`
	Matrix       string                  `json:"matrix,omitempty"`
}

// EstSaving returns the estimated saving range in bytes (low, high).
func (p Plan) EstSaving() (int64, int64) {
	return max(p.SourceSize-p.EstMax, 0), max(p.SourceSize-p.EstMin, 0)
}

// Decision is the full outcome for one item.
type Decision struct {
	Outcome    Outcome  `json:"outcome"`
	PolicyID   int64    `json:"policy_id,omitempty"`
	PolicyName string   `json:"policy_name,omitempty"`
	Summary    string   `json:"summary"`
	Reasons    []Reason `json:"reasons,omitempty"`
	Plan       *Plan    `json:"plan,omitempty"`
}

// Decide turns a policy result into a decision.
func Decide(it policy.Item, res policy.Result, facts FileFacts, env Env) Decision {
	if res.Winner == nil {
		return Decision{Outcome: NoPolicy, Summary: "No enabled policy matches this item."}
	}
	w := res.Winner
	d := Decision{PolicyID: w.ID, PolicyName: w.Name}
	if w.Action.Kind == policy.KindProtect {
		d.Outcome = Protected
		d.Summary = "Protected by " + w.Name + "."
		return d
	}
	if reasons := safetyChecks(it.File, facts, w.Action, env); len(reasons) > 0 {
		d.Outcome = Skipped
		d.Reasons = reasons
		d.Summary = "Skipped: " + reasons[0].Text
		return d
	}
	return decideTarget(d, it.File, facts, w.Action, env)
}

func sourceLabel(v media.VideoStream) string {
	return v.Resolution().Label() + " " + v.Codec.Label()
}
