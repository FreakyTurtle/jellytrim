package plan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/freakyturtle/jellytrim/internal/media"
	"github.com/freakyturtle/jellytrim/internal/policy"
)

type fakeEncoders struct{ none bool }

func (f fakeEncoders) Select(c media.Codec, _ bool, _ string) (string, bool, string) {
	if f.none {
		return "", false, "No working encoder for " + c.Label() + " was found."
	}
	return "x265", true, ""
}

var env = Env{MinSavingPercent: 10, DefaultQuality: policy.QualityHigh, Encoders: fakeEncoders{}}

var okFacts = FileFacts{Probed: true, Nlink: 1, InRoots: true}

func load(t *testing.T, name string) *media.File {
	t.Helper()
	dir := filepath.Join("..", "media", "testdata", "probe")
	probe, err := os.ReadFile(filepath.Join(dir, name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	frames, _ := os.ReadFile(filepath.Join(dir, name+".frames.json"))
	f, err := media.Parse(probe, frames, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	// The clips are a few seconds long; scale the size and duration up to a
	// film so estimates behave like real files while keeping the bitrate.
	if bps, ok := f.VideoBitrate(); ok {
		f.Duration = 2 * time.Hour
		f.Size = int64(float64(bps)*f.Duration.Seconds()/8) + 500_000_000
	}
	return f
}

func winner(a policy.Action) policy.Result {
	p := &policy.Policy{ID: 7, Name: "Test policy", Enabled: true, Action: a}
	return policy.Result{Winner: p}
}

func hevc(maxRes string) policy.Action {
	return policy.Action{Kind: policy.KindOptimise, MaxResolution: maxRes, Codec: "hevc", Quality: policy.QualityHigh}
}

func decide(t *testing.T, fixture string, a policy.Action, facts FileFacts, e Env) Decision {
	t.Helper()
	return Decide(policy.Item{File: load(t, fixture)}, winner(a), facts, e)
}

func TestDecisions(t *testing.T) {
	cases := []struct {
		name, fixture string
		action        policy.Action
		want          Outcome
		code          string // a reason code that must be present
		target        string
	}{
		{"inefficient H.264 to HEVC", "h264-1080p", hevc("keep"), Optimise, "", "1080p HEVC"},
		{"HEVC already optimal", "hevc-1080p", hevc("keep"), AlreadyOptimal, "optimal", ""},
		{"never convert to a less efficient codec", "hevc-1080p", policy.Action{Kind: policy.KindOptimise, Codec: "h264"}, AlreadyOptimal, "optimal", ""},
		{"4K H.264 to 1080p HEVC", "h264-2160p", hevc("1080p"), Optimise, "", "1080p HEVC"},
		{"HDR10 keeps HDR", "hevc-2160p-hdr10", hevc("1080p"), Optimise, "", "1080p HEVC"},
		{"HLG keeps HLG", "hevc-1080p-hlg", hevc("720p"), Optimise, "", "720p HEVC"},
		{"never upscale", "h264-2160p", policy.Action{Kind: policy.KindOptimise, MaxResolution: "2160p", Codec: "keep"}, AlreadyOptimal, "optimal", ""},
		{"interlaced skipped", "interlaced", hevc("keep"), Skipped, "interlaced", ""},
		{"MP4 with mov_text and cover art", "mp4-movtext-cover", hevc("keep"), Optimise, "", "1080p HEVC"},
		{"MKV with ASS and fonts", "anime-ass", hevc("keep"), Optimise, "", "720p HEVC"},
		{"multi-audio and subtitles", "multi-audio-subs", hevc("keep"), Optimise, "", "1080p HEVC"},
		{"scope film downscaled", "scope-1080p", hevc("720p"), Optimise, "", "720p HEVC"},
		{"AV1 left alone by an HEVC policy", "av1-1080p", hevc("keep"), AlreadyOptimal, "optimal", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := decide(t, tc.fixture, tc.action, okFacts, env)
			if d.Outcome != tc.want {
				t.Fatalf("outcome %s, want %s (%s) %+v", d.Outcome, tc.want, d.Summary, d.Reasons)
			}
			if tc.code != "" && !hasCode(d, tc.code) {
				t.Fatalf("missing reason %q: %+v", tc.code, d.Reasons)
			}
			if tc.target != "" && d.Plan.TargetLabel != tc.target {
				t.Fatalf("target %q, want %q", d.Plan.TargetLabel, tc.target)
			}
		})
	}
}

func hasCode(d Decision, code string) bool {
	for _, r := range d.Reasons {
		if r.Code == code {
			return true
		}
	}
	return false
}

func TestHDR10PlanCarriesMetadata(t *testing.T) {
	d := decide(t, "hevc-2160p-hdr10", hevc("1080p"), okFacts, env)
	p := d.Plan
	if p.HDR != media.HDR10 || p.BitDepth != 10 || p.Mastering == nil || p.ContentLight == nil {
		t.Fatalf("HDR10 plan lost metadata: %+v", p)
	}
	if p.Transfer != "smpte2084" || p.Primaries != "bt2020" {
		t.Fatalf("colour tags: %q %q", p.Transfer, p.Primaries)
	}
	if p.Width != 1920 || p.Height != 1080 || !p.Scale {
		t.Fatalf("size %dx%d scale %v", p.Width, p.Height, p.Scale)
	}
}

func TestScopeAspectKept(t *testing.T) {
	d := decide(t, "scope-1080p", hevc("720p"), okFacts, env)
	if d.Plan.Width != 1280 || d.Plan.Height != 532 {
		t.Fatalf("scope film scaled to %dx%d, want 1280x532", d.Plan.Width, d.Plan.Height)
	}
}

func TestCopyIndicesKeepEverything(t *testing.T) {
	d := decide(t, "multi-audio-subs", hevc("keep"), okFacts, env)
	if got := d.Plan.CopyIndices; len(got) != 6 || got[0] != 1 || got[5] != 6 {
		t.Fatalf("copy indices %v, want 1..6", got)
	}
	d = decide(t, "mp4-movtext-cover", hevc("keep"), okFacts, env)
	if got := d.Plan.CopyIndices; len(got) != 3 || got[2] != 3 {
		t.Fatalf("mp4 copy indices %v, want [1 2 3] with cover art", got)
	}
}

func TestFileFactsSkipWithEveryReason(t *testing.T) {
	facts := FileFacts{Probed: true, IsSymlink: true, Nlink: 2, InRoots: false}
	d := decide(t, "h264-1080p", hevc("keep"), facts, env)
	if d.Outcome != Skipped {
		t.Fatalf("outcome %s", d.Outcome)
	}
	for _, c := range []string{"symlink", "hardlink", "outside_roots"} {
		if !hasCode(d, c) {
			t.Errorf("missing %s", c)
		}
	}
}

func TestUnprobedAndFailedProbe(t *testing.T) {
	d := Decide(policy.Item{}, winner(hevc("keep")), FileFacts{InRoots: true, Nlink: 1}, env)
	if d.Outcome != Skipped || !hasCode(d, "not_probed") {
		t.Fatalf("unprobed: %+v", d)
	}
	d = Decide(policy.Item{}, winner(hevc("keep")), FileFacts{ProbeError: "Invalid data found"}, env)
	if !hasCode(d, "probe_failed") || !strings.Contains(d.Summary, "Invalid data") {
		t.Fatalf("probe failure: %+v", d)
	}
}

func TestNoPolicyAndProtect(t *testing.T) {
	f := load(t, "h264-1080p")
	if d := Decide(policy.Item{File: f}, policy.Result{}, okFacts, env); d.Outcome != NoPolicy {
		t.Fatalf("no winner: %s", d.Outcome)
	}
	d := Decide(policy.Item{File: f}, winner(policy.Action{Kind: policy.KindProtect}), okFacts, env)
	if d.Outcome != Protected || d.Summary != "Protected by Test policy." {
		t.Fatalf("protect: %+v", d)
	}
}

func TestOptimisedLoopGuard(t *testing.T) {
	facts := okFacts
	facts.Optimised = true
	if d := decide(t, "h264-1080p", hevc("keep"), facts, env); d.Outcome != AlreadyOptimal {
		t.Fatalf("re-encoding a JellyTrim file at the same resolution: %s", d.Outcome)
	}
	if d := decide(t, "h264-2160p", hevc("1080p"), facts, env); d.Outcome != Optimise {
		t.Fatalf("a further downscale should still be allowed: %s", d.Outcome)
	}
}

func TestMinimumSaving(t *testing.T) {
	e := env
	e.MinSavingPercent = 95
	d := decide(t, "h264-1080p", hevc("keep"), okFacts, e)
	if d.Outcome != AlreadyOptimal || !strings.Contains(d.Summary, "below the 95% minimum") {
		t.Fatalf("min saving: %+v", d)
	}
}

func TestNoEncoder(t *testing.T) {
	e := env
	e.Encoders = fakeEncoders{none: true}
	d := decide(t, "h264-1080p", hevc("keep"), okFacts, e)
	if d.Outcome != Skipped || !hasCode(d, "no_encoder") {
		t.Fatalf("no encoder: %+v", d)
	}
}

func TestEstimatesAreSane(t *testing.T) {
	d := decide(t, "h264-2160p", hevc("1080p"), okFacts, env)
	p := d.Plan
	if p.EstMin <= 0 || p.EstMin > p.EstMax || p.EstMax > p.SourceSize {
		t.Fatalf("estimate %d..%d for source %d", p.EstMin, p.EstMax, p.SourceSize)
	}
	lo, hi := p.EstSaving()
	if lo < 0 || hi < lo {
		t.Fatalf("saving %d..%d", lo, hi)
	}
	if !strings.Contains(d.Summary, "(estimate)") {
		t.Fatalf("summary must say it is an estimate: %q", d.Summary)
	}
}
