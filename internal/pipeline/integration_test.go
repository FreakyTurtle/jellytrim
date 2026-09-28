package pipeline

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"

	"github.com/freakyturtle/jellytrim/internal/encoder"
	"github.com/freakyturtle/jellytrim/internal/ffmpeg"
	"github.com/freakyturtle/jellytrim/internal/fileid"
	"github.com/freakyturtle/jellytrim/internal/media"
	"github.com/freakyturtle/jellytrim/internal/plan"
	"github.com/freakyturtle/jellytrim/internal/policy"
	"github.com/freakyturtle/jellytrim/internal/testutil"
)

// TestPipelineRealEncode runs the whole pipeline with real ffmpeg and x265
// on copies of the synthetic fixtures.
func TestPipelineRealEncode(t *testing.T) {
	ffmpegPath, ffprobePath := testutil.RequireFFmpeg(t)
	runner := ffmpeg.New(ffmpegPath, ffprobePath, nil)
	reg := encoder.NewRegistry(encoder.DefaultBackends(encoder.DefaultQSVDevice))
	reg.Detect(context.Background(), runner)

	cases := []struct {
		fixture string
		action  policy.Action
		hdr     media.HDRClass
	}{
		{"h264-1080p", policy.Action{Kind: policy.KindOptimise, MaxResolution: "keep", Codec: "hevc", Quality: policy.QualitySpaceSaver}, media.SDR},
		{"hevc-2160p-hdr10", policy.Action{Kind: policy.KindOptimise, MaxResolution: "1080p", Codec: "hevc", Quality: policy.QualitySpaceSaver}, media.HDR10},
		{"multi-audio-subs", policy.Action{Kind: policy.KindOptimise, MaxResolution: "720p", Codec: "hevc", Quality: policy.QualitySpaceSaver}, media.SDR},
		{"mp4-movtext-cover", policy.Action{Kind: policy.KindOptimise, MaxResolution: "720p", Codec: "hevc", Quality: policy.QualitySpaceSaver}, media.SDR},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			ctx := context.Background()
			path := testutil.CopyFixture(t, tc.fixture)
			orig, _ := os.ReadFile(path)
			probe, frames, err := runner.Probe(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			info, err := fileid.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			src, err := media.Parse(probe, frames, info.Size)
			if err != nil {
				t.Fatal(err)
			}
			d := plan.Decide(policy.Item{File: src},
				policy.Result{Winner: &policy.Policy{ID: 1, Name: "t", Enabled: true, Action: tc.action}},
				plan.FileFacts{Probed: true, Nlink: 1, InRoots: true},
				plan.Env{MinSavingPercent: 1, Encoders: reg})
			if d.Outcome != plan.Optimise {
				t.Fatalf("decision %s: %s %+v", d.Outcome, d.Summary, d.Reasons)
			}
			backend, ok := reg.Backend(d.Plan.Encoder)
			if !ok {
				t.Fatalf("no backend %q", d.Plan.Encoder)
			}
			root, _ := filepath.EvalSymlinks(filepath.Dir(path))
			j := Job{
				ID: 7, Path: path, Plan: *d.Plan, Source: src, Identity: info, Encoder: backend.Label(),
				Roots: []string{filepath.ToSlash(root)}, MinSavingPercent: 1,
				Args: func(in, out string) ([]string, error) {
					return encoder.BuildArgs(backend, encoder.Job{Plan: *d.Plan, Source: src, Input: in, Output: out, JobID: 7})
				},
			}
			journal := &memJournal{}
			p := &Pipeline{FS: OSFS{}, Runner: runner, Journal: journal}
			res := p.Execute(ctx, j, Hooks{})
			if res.Outcome != Complete {
				t.Fatalf("%s: %s\n%+v", res.Outcome, res.Summary, res.Diagnostics)
			}
			for _, c := range res.Diagnostics.Checks {
				if !c.Pass {
					t.Errorf("check %s failed: %s", c.Name, c.Detail)
				}
			}
			backup, _ := os.ReadFile(res.BackupPath)
			if sha256.Sum256(backup) != sha256.Sum256(orig) {
				t.Fatal("backup does not match the original")
			}
			outProbe, outFrames, err := runner.Probe(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			out, err := media.Parse(outProbe, outFrames, 0)
			if err != nil {
				t.Fatal(err)
			}
			v, _ := out.MainVideo()
			if v.Codec != media.CodecHEVC || out.HDR().Class != tc.hdr {
				t.Fatalf("output %s %s", v.Codec, out.HDR().Class)
			}
		})
	}
}
