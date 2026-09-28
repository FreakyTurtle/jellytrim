package encoder

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/freakyturtle/jellytrim/internal/media"
	"github.com/freakyturtle/jellytrim/internal/plan"
	"github.com/freakyturtle/jellytrim/internal/policy"
)

var update = flag.Bool("update", false, "rewrite golden files")

type goldenCase struct {
	name    string
	backend Backend
	job     func(t *testing.T) Job
}

func fixtureJob(fixture string, w, h int, quality string, edit func(*Job)) func(t *testing.T) Job {
	return func(t *testing.T) Job {
		f := loadFixture(t, fixture)
		j := jobFor(f, planFor(t, f, w, h, quality))
		if edit != nil {
			edit(&j)
		}
		return j
	}
}

func goldenCases() []goldenCase {
	var cases []goldenCase
	for _, tier := range tiers {
		cases = append(cases, goldenCase{"x265-" + tier, hdrX265(), fixtureJob("h264-1080p", 0, 0, tier, nil)})
	}
	high := policy.QualityHigh
	return append(cases,
		goldenCase{"x265-scale-1080p-to-720p", hdrX265(), fixtureJob("h264-1080p", 1280, 720, high, nil)},
		goldenCase{"x265-scope-to-720p", hdrX265(), fixtureJob("scope-1080p", 1280, 532, high, nil)},
		goldenCase{"x265-hdr10-2160p-to-1080p", hdrX265(), fixtureJob("hevc-2160p-hdr10", 1920, 1080, high, nil)},
		goldenCase{"x265-hlg", hdrX265(), fixtureJob("hevc-1080p-hlg", 0, 0, high, nil)},
		goldenCase{"x265-8bit-match", hdrX265(), fixtureJob("h264-1080p", 0, 0, high, func(j *Job) { j.Plan.BitDepth = 8 })},
		goldenCase{"x265-mp4-movtext-cover", hdrX265(), fixtureJob("mp4-movtext-cover", 0, 0, high, nil)},
		goldenCase{"x265-multi-audio-subs", hdrX265(), fixtureJob("multi-audio-subs", 0, 0, high, nil)},
		goldenCase{"x265-ass-attachment", hdrX265(), fixtureJob("anime-ass", 0, 0, high, nil)},
		goldenCase{"x265-reduce-hdr-dv8", hdrX265(), fixtureJob("dv-profile8-hdr10", 0, 0, high, nil)},
		goldenCase{"x265-overrides", hdrX265(), fixtureJob("h264-1080p", 0, 0, high, func(j *Job) {
			j.Overrides = Overrides{Preset: "slower", Threads: 4, Quality: map[string]map[string]int{NameX265: {high: 20}}}
		})},
		goldenCase{"qsv-1080p-keep", qsvWith(false, "h264/8"), fixtureJob("h264-1080p", 0, 0, high, nil)},
		goldenCase{"qsv-scale-2160p-to-1080p-10bit", qsvWith(false, "h264/8"), fixtureJob("h264-2160p", 1920, 1080, high, nil)},
		goldenCase{"qsv-sw-decode-fallback", qsvWith(false, "h264/8", "hevc/10"), fixtureJob("av1-1080p", 0, 0, high, nil)},
		goldenCase{"qsv-hdr10", qsvWith(true, "hevc/10"), fixtureJob("hevc-2160p-hdr10", 0, 0, high, nil)},
	)
}

// TestBuildArgsGolden compares every argument list with testdata/<name>.golden
// (one option and its value per line). Regenerate with -update and review
// each line.
func TestBuildArgsGolden(t *testing.T) {
	for _, tc := range goldenCases() {
		t.Run(tc.name, func(t *testing.T) {
			args, err := BuildArgs(tc.backend, tc.job(t))
			if err != nil {
				t.Fatal(err)
			}
			got := goldenFormat(args)
			file := filepath.Join("testdata", tc.name+".golden")
			if *update {
				if err := os.WriteFile(file, []byte(got), 0o600); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("%v (run with -update to create it)", err)
			}
			if got != string(want) {
				t.Errorf("arguments differ from %s:\n%s", file, lineDiff(string(want), got))
			}
		})
	}
}

// goldenFormat puts each option on its own line with its value. An option
// is "-" followed by a letter; a value never is ("-1" is a value).
func goldenFormat(args []string) string {
	var b strings.Builder
	for i := 0; i < len(args); i++ {
		b.WriteString(args[i])
		if isOption(args[i]) && i+1 < len(args) && !isOption(args[i+1]) {
			b.WriteString(" " + args[i+1])
			i++
		}
		b.WriteString("\n")
	}
	return b.String()
}

func isOption(a string) bool {
	return len(a) > 1 && a[0] == '-' && (a[1] >= 'a' && a[1] <= 'z' || a[1] >= 'A' && a[1] <= 'Z')
}

func lineDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	var b strings.Builder
	for i := range max(len(w), len(g)) {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			fmt.Fprintf(&b, "line %d: want %s\n         got  %s\n", i+1, wl, gl)
		}
	}
	return b.String()
}

func TestBuildArgsInvariants(t *testing.T) {
	for _, tc := range goldenCases() {
		t.Run(tc.name, func(t *testing.T) {
			j := tc.job(t)
			args, err := BuildArgs(tc.backend, j)
			if err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(args, " ")
			for _, want := range []string{
				"-nostdin -hide_banner -loglevel error -progress pipe:1 -nostats -y",
				"-i file:" + j.Input,
				"-c copy -c:v:0 ",
				"-disposition:v:0 ",
			} {
				if !strings.Contains(joined, want) {
					t.Errorf("missing %q", want)
				}
			}
			tag := "-metadata JELLYTRIM=v1;enc=" + tc.backend.Name() + ";q=" + j.Plan.Quality
			if strings.Contains(joined, tag) != (j.Plan.Container == media.Matroska) {
				t.Errorf("JELLYTRIM tag present = %v for %s", j.Plan.Container != media.Matroska, j.Plan.Container)
			}
			if args[len(args)-1] != "file:"+j.Output {
				t.Errorf("output is %q", args[len(args)-1])
			}
			// Every source stream is mapped exactly once.
			maps := 0
			for i, a := range args {
				if a == "-map" && strings.HasPrefix(args[i+1], "0:") {
					maps++
				}
			}
			if maps != j.Source.StreamCount {
				t.Errorf("%d maps for %d streams", maps, j.Source.StreamCount)
			}
		})
	}
}

func TestBuildArgsRefuses(t *testing.T) {
	cases := []struct {
		name    string
		backend Backend
		fixture string
		edit    func(*Job)
		want    string
	}{
		{"odd width", hdrX265(), "h264-1080p", func(j *Job) { j.Plan.Width, j.Plan.Scale = 1279, true }, "even"},
		{"zero height", hdrX265(), "h264-1080p", func(j *Job) { j.Plan.Height = 0 }, "even"},
		{"other container", hdrX265(), "h264-1080p", func(j *Job) { j.Plan.Container = media.OtherContainer }, "Matroska or MP4"},
		{"HDR without proven passthrough", X265{Cap: Capability{Available: true}}, "hevc-2160p-hdr10", nil, "HDR metadata"},
		{"HDR on 8-bit", hdrX265(), "hevc-2160p-hdr10", func(j *Job) { j.Plan.BitDepth = 8 }, "10-bit"},
		{"Dolby Vision without reduction", hdrX265(), "dv-profile8-hdr10", func(j *Job) { j.Plan.HDR = media.DolbyVisionHDR10; j.Plan.ReduceHDR = false }, "SDR, HDR10 or HLG"},
		{"reduction without -dolbyvision", X265{Cap: Capability{HDRPassthrough: true}}, "dv-profile8-hdr10", nil, "Dolby Vision or HDR10+"},
		{"reduction on QSV", qsvWith(true, "hevc/10"), "dv-profile8-hdr10", nil, "Dolby Vision or HDR10+"},
		{"HDR10 plan with SDR tags", hdrX265(), "hevc-2160p-hdr10", func(j *Job) { j.Plan.Transfer = "bt709" }, "needs transfer smpte2084"},
		{"PQ tags on an SDR plan", hdrX265(), "hevc-2160p-hdr10", func(j *Job) { j.Plan.HDR = media.SDR }, "needs an HDR plan"},
		{"unknown colour value", hdrX265(), "h264-1080p", func(j *Job) { j.Plan.Transfer = "linear" }, "cannot be carried over"},
		{"stream dropped", hdrX265(), "multi-audio-subs", func(j *Job) { j.Plan.CopyIndices = j.Plan.CopyIndices[1:] }, "would be dropped"},
		{"stream copied twice", hdrX265(), "multi-audio-subs", func(j *Job) { j.Plan.CopyIndices = append(j.Plan.CopyIndices, 1) }, "not a stream to copy"},
		{"main video copied", hdrX265(), "h264-1080p", func(j *Job) { j.Plan.CopyIndices = append(j.Plan.CopyIndices, 0) }, "not a stream to copy"},
		{"cover art as main video", hdrX265(), "mp4-movtext-cover", func(j *Job) { j.Plan.VideoIndex = 3 }, "main video"},
		{"upscale", hdrX265(), "h264-1080p", func(j *Job) { j.Plan.Width, j.Plan.Height, j.Plan.Scale = 3840, 2160, true }, "upscale"},
		{"resize without scale", hdrX265(), "h264-1080p", func(j *Job) { j.Plan.Width, j.Plan.Height = 1280, 720 }, "without scaling"},
		{"data stream", hdrX265(), "data-stream-mp4", nil, "data stream"},
		{"unknown tier", hdrX265(), "h264-1080p", func(j *Job) { j.Plan.Quality = "ultra" }, "quality tier"},
		{"bad preset", hdrX265(), "h264-1080p", func(j *Job) { j.Overrides.Preset = "ultrafast" }, "preset"},
		{"wrong codec", hdrX265(), "h264-1080p", func(j *Job) { j.Plan.Codec = media.CodecAV1 }, "produces HEVC"},
		{"output over input", hdrX265(), "h264-1080p", func(j *Job) { j.Output = j.Input }, "output path"},
		{"no source", hdrX265(), "h264-1080p", func(j *Job) { j.Source = nil }, "probed source"},
		{"bad QSV device", QSV{Device: "/dev/dri/renderD128,driver=x", Cap: Capability{Available: true}}, "h264-1080p", nil, "render node"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := loadFixture(t, tc.fixture)
			j := jobFor(f, planFor(t, f, 0, 0, policy.QualityHigh))
			if tc.edit != nil {
				tc.edit(&j)
			}
			args, err := BuildArgs(tc.backend, j)
			if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want refusal containing %q (args %v)", err, tc.want, args)
			}
		})
	}
}

func TestQualityValues(t *testing.T) {
	want := map[string][2]int{
		policy.QualityMaximum: {18, 19}, policy.QualityHigh: {21, 22},
		policy.QualityBalanced: {24, 25}, policy.QualitySpaceSaver: {27, 28},
	}
	for tier, v := range want {
		if got := (X265{}).QualityValue(tier, Overrides{}); got != v[0] {
			t.Errorf("x265 %s = %d, want %d", tier, got, v[0])
		}
		if got := (QSV{}).QualityValue(tier, Overrides{}); got != v[1] {
			t.Errorf("qsv %s = %d, want %d", tier, got, v[1])
		}
	}
	o := Overrides{Quality: map[string]map[string]int{NameX265: {policy.QualityHigh: 20, policy.QualityBalanced: 99}}}
	if got := (X265{}).QualityValue(policy.QualityHigh, o); got != 20 {
		t.Errorf("override ignored: %d", got)
	}
	if got := (X265{}).QualityValue(policy.QualityBalanced, o); got != 24 {
		t.Errorf("out-of-range override used: %d", got)
	}
	if got := (QSV{}).QualityValue(policy.QualityHigh, o); got != 22 {
		t.Errorf("x265 override applied to QSV: %d", got)
	}
	if (X265{}).QualityValue("ultra", o) != 0 {
		t.Error("unknown tier has a value")
	}
	if err := o.Validate(); err == nil {
		t.Error("Validate accepted CRF 99")
	}
}

// TestDecidedPlansBuild checks the contract with internal/plan: every plan
// Decide produces from the fixtures builds with x265.
func TestDecidedPlansBuild(t *testing.T) {
	names, err := filepath.Glob(filepath.Join("..", "media", "testdata", "probe", "*.frames.json"))
	if err != nil {
		t.Fatal(err)
	}
	env := plan.Env{MinSavingPercent: 10, DefaultQuality: policy.QualityHigh, Encoders: registryWith(hdrX265())}
	facts := plan.FileFacts{Probed: true, Nlink: 1, InRoots: true}
	built := 0
	for _, n := range names {
		name := strings.TrimSuffix(filepath.Base(n), ".frames.json")
		for _, res := range []string{"keep", "1080p", "720p"} {
			f := loadFixture(t, name)
			stretch(f)
			a := policy.Action{Kind: policy.KindOptimise, MaxResolution: res, Codec: "hevc", Quality: policy.QualityHigh, AllowHDRReduction: true}
			d := plan.Decide(policy.Item{File: f}, policy.Result{Winner: &policy.Policy{ID: 1, Name: "p", Enabled: true, Action: a}}, facts, env)
			if d.Outcome != plan.Optimise {
				continue
			}
			if _, err := BuildArgs(hdrX265(), jobFor(f, *d.Plan)); err != nil {
				t.Errorf("%s at %s: %v", name, res, err)
			}
			built++
		}
	}
	if built < 10 {
		t.Errorf("only %d plans were built; the fixtures should give more", built)
	}
}

// stretch makes a few-second clip look like a film so the worth-it checks
// behave as they would on real files, keeping the bitrate.
func stretch(f *media.File) {
	if bps, ok := f.VideoBitrate(); ok {
		f.Duration = 2 * time.Hour
		f.Size = int64(float64(bps)*f.Duration.Seconds()/8) + 500_000_000
	}
}

func registryWith(bs ...Backend) *Registry {
	r := NewRegistry(bs)
	caps := make([]Capability, 0, len(bs))
	for _, b := range bs {
		caps = append(caps, b.Capability())
	}
	r.SetCapabilities(caps)
	return r
}
