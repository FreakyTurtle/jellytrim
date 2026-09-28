package encoder

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/freakyturtle/jellytrim/internal/ffmpeg"
	"github.com/freakyturtle/jellytrim/internal/media"
	"github.com/freakyturtle/jellytrim/internal/plan"
	"github.com/freakyturtle/jellytrim/internal/policy"
)

// The detection test clip: one second of ffmpeg's test pattern.
const (
	testWidth    = 1280
	testHeight   = 720
	testDuration = time.Second
	testSource   = "testsrc2=size=1280x720:rate=24:duration=1"
	maxDetail    = 600
)

// Reference HDR10 metadata for the passthrough test: BT.2020 primaries, a
// D65 white point, 1000 cd/m² mastering and MaxCLL 1000, MaxFALL 400.
var (
	refMastering = media.MasteringDisplay{
		GreenX: 8500, GreenY: 39850, BlueX: 6550, BlueY: 2300, RedX: 35400, RedY: 14600,
		WhiteX: 15635, WhiteY: 16450, MaxLuminance: 10000000, MinLuminance: 50,
	}
	refContentLight = media.ContentLight{MaxCLL: 1000, MaxFALL: 400}
)

// detectEnv is what every backend's detection shares.
type detectEnv struct {
	runner   *ffmpeg.Runner
	version  string
	encoders map[string]bool
	listErr  error
	dir      string
}

// Detect proves what each backend can do on this machine with real test
// encodes (docs/TRANSCODING.md, "Hardware probe"). It never fails as a
// whole: a backend that cannot be tested is reported unavailable with a
// plain-English Error and the technical Detail.
func Detect(ctx context.Context, runner *ffmpeg.Runner, backends []Backend) []Capability {
	env := detectEnv{runner: runner}
	env.version, _ = runner.Version(ctx)
	env.encoders, env.listErr = runner.EncoderNames(ctx)
	now := time.Now().UTC()
	caps := make([]Capability, 0, len(backends))
	for _, b := range backends {
		c := detectBackend(ctx, env, b)
		c.TestedAt = now
		if runner.Log != nil {
			runner.Log.Info("encoder: detected", "backend", c.Backend, "available", c.Available,
				"hdr", c.HDRPassthrough, "error", c.Error)
		}
		caps = append(caps, c)
	}
	return caps
}

func detectBackend(ctx context.Context, env detectEnv, b Backend) Capability {
	c := capabilityFor(b)
	c.FFmpegVersion = env.version
	if q, ok := b.(QSV); ok {
		c.Device = q.device()
		if _, err := os.Stat(q.device()); err != nil {
			c.Error = fmt.Sprintf("No Intel GPU device (%s) found.", q.device())
			return c
		}
	}
	if env.listErr != nil {
		c.Error = "ffmpeg could not be run."
		c.Detail = clip(env.listErr.Error())
		return c
	}
	if !env.encoders[b.FFmpegEncoder()] {
		c.Error = fmt.Sprintf("This ffmpeg does not include the %s encoder.", b.FFmpegEncoder())
		return c
	}
	if b.Name() == NameX265 {
		help, err := env.runner.EncoderHelp(ctx, "libx265")
		c.DolbyVisionOption = err == nil && strings.Contains(help, "-dolbyvision ")
	}
	dir, err := os.MkdirTemp("", "jellytrim-detect-*")
	if err != nil {
		c.Error = "JellyTrim could not create a temporary folder for the test encode."
		c.Detail = clip(err.Error())
		return c
	}
	defer func() { _ = os.RemoveAll(dir) }()
	env.dir = dir
	return runDetection(ctx, env, b, c)
}

// runDetection runs the test encode, then (for hardware) the decode tests,
// then the HDR passthrough test.
func runDetection(ctx context.Context, env detectEnv, b Backend, c Capability) Capability {
	b = b.WithCapability(c)
	took, err := testEncode(ctx, env, b)
	if err != nil {
		c.Error = "The test encode failed."
		c.Detail = clip(err.Error())
		return c
	}
	c.Available = true
	details := []string{fmt.Sprintf("Test encode passed in %.1f s.", took.Seconds())}
	if q, ok := b.(QSV); ok {
		c.HWDecode = testHWDecode(ctx, env, q)
		details = append(details, "Hardware decoding: "+listOrNone(c.HWDecode)+".")
	}
	b = b.WithCapability(c)
	if err := testHDR(ctx, env, b); err != nil {
		details = append(details, "HDR10 metadata was not kept: "+err.Error())
	} else {
		c.HDRPassthrough = true
		details = append(details, "HDR10 metadata kept.")
	}
	c.Detail = clip(strings.Join(details, " "))
	return c
}

// testJob is a 1280x720 encode with the settings real jobs use.
func testJob(b Backend, out string, hdr bool) Job {
	p := plan.Plan{
		Codec: b.Codec(), Encoder: b.Name(), Quality: policy.QualityHigh,
		Width: testWidth, Height: testHeight, BitDepth: 10, HDR: media.SDR,
		Container: media.Matroska, VideoIndex: 0,
	}
	src := &media.File{
		Container: media.Matroska, Duration: testDuration,
		Video: []media.VideoStream{{Index: 0, Codec: "rawvideo", Width: testWidth, Height: testHeight, BitDepth: 8, SAR: "1:1"}},
	}
	if hdr {
		p.HDR, p.Primaries, p.Transfer, p.Matrix = media.HDR10, "bt2020", "smpte2084", "bt2020nc"
		m, cl := refMastering, refContentLight
		p.Mastering, p.ContentLight = &m, &cl
	}
	return Job{Plan: p, Source: src, Output: out}
}

var lavfiInput = []string{"-f", "lavfi", "-i", testSource}

// testEncode encodes the test pattern and checks the output's codec and
// size.
func testEncode(ctx context.Context, env detectEnv, b Backend) (time.Duration, error) {
	out := filepath.Join(env.dir, "test.mkv")
	args, err := build(b, testJob(b, out, false), buildMode{input: lavfiInput, detecting: true})
	if err != nil {
		return 0, err
	}
	res, err := run(ctx, env.runner, args)
	if err != nil {
		return 0, err
	}
	f, err := probeFile(ctx, env.runner, out)
	if err != nil {
		return 0, err
	}
	v, ok := f.MainVideo()
	if !ok || v.Codec != b.Codec() || v.Width != testWidth || v.Height != testHeight {
		return 0, fmt.Errorf("the test output is %s %dx%d, not %s %dx%d", v.Codec, v.Width, v.Height, b.Codec(), testWidth, testHeight)
	}
	return res.Duration, nil
}

// testHDR encodes an HDR10 clip and checks that the output keeps the PQ
// tags and the static metadata. x265 encodes the test pattern with its HDR
// options; a hardware backend re-encodes an x265-made HDR10 clip, because
// it carries the metadata from the decoded frames.
func testHDR(ctx context.Context, env detectEnv, b Backend) error {
	out := filepath.Join(env.dir, "hdr.mkv")
	var args []string
	var err error
	if b.Hardware() {
		args, err = hardwareHDRArgs(ctx, env, b, out)
	} else {
		args, err = build(b, testJob(b, out, true), buildMode{input: lavfiInput, detecting: true})
	}
	if err != nil {
		return err
	}
	if _, err := run(ctx, env.runner, args); err != nil {
		return err
	}
	f, err := probeFile(ctx, env.runner, out)
	if err != nil {
		return err
	}
	return checkHDR10(f)
}

func hardwareHDRArgs(ctx context.Context, env detectEnv, b Backend, out string) ([]string, error) {
	if !env.encoders["libx265"] {
		return nil, errors.New("no libx265 to make the HDR10 test clip")
	}
	src := filepath.Join(env.dir, "hdr-source.mkv")
	x := X265{}
	srcArgs, err := build(x, testJob(x, src, true), buildMode{input: lavfiInput, detecting: true})
	if err != nil {
		return nil, err
	}
	if _, err := run(ctx, env.runner, srcArgs); err != nil {
		return nil, fmt.Errorf("making the HDR10 test clip: %w", err)
	}
	f, err := probeFile(ctx, env.runner, src)
	if err != nil {
		return nil, err
	}
	j := testJob(b, out, true)
	j.Source, j.Input = f, src
	j.Plan.VideoIndex = 0
	return build(b, j, buildMode{detecting: true})
}

// checkHDR10 compares the output with the reference metadata.
func checkHDR10(f *media.File) error {
	v, ok := f.MainVideo()
	switch {
	case !ok:
		return errors.New("the output has no video")
	case v.Transfer != "smpte2084" || v.Primaries != "bt2020" || v.Matrix != "bt2020nc":
		return fmt.Errorf("the output is tagged %q/%q/%q", v.Primaries, v.Transfer, v.Matrix)
	case v.Mastering == nil || *v.Mastering != refMastering:
		return errors.New("the mastering display metadata is missing or changed")
	case v.ContentLight == nil || *v.ContentLight != refContentLight:
		return errors.New("the content light level metadata is missing or changed")
	}
	return nil
}

// hwDecodeTests are the sources tried on the GPU: the software encoder that
// makes the clip, and its pixel format.
var hwDecodeTests = []struct {
	codec   media.Codec
	depth   int
	encoder string
	pixFmt  string
}{
	{media.CodecH264, 8, "libx264", "yuv420p"},
	{media.CodecHEVC, 8, "libx265", "yuv420p"},
	{media.CodecHEVC, 10, "libx265", "yuv420p10le"},
	{media.CodecAV1, 8, "libsvtav1", "yuv420p"},
	{media.CodecAV1, 10, "libsvtav1", "yuv420p10le"},
}

// testHWDecode lists the sources the GPU decodes. Each is a small software
// clip decoded with QSV and downloaded; hwdownload fails if ffmpeg quietly
// fell back to software decoding.
func testHWDecode(ctx context.Context, env detectEnv, q QSV) []string {
	var ok []string
	for i, tc := range hwDecodeTests {
		if !env.encoders[tc.encoder] {
			continue
		}
		clipPath := filepath.Join(env.dir, fmt.Sprintf("decode-%d.mkv", i))
		mk := append(append([]string{}, commonPrefix...),
			"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=24:duration=0.5",
			"-c:v", tc.encoder, "-pix_fmt", tc.pixFmt, "-f", "matroska", "file:"+clipPath)
		if _, err := run(ctx, env.runner, mk); err != nil {
			continue
		}
		download := "hwdownload,format=nv12"
		if tc.depth == 10 {
			download = "hwdownload,format=p010le"
		}
		dec := append(append([]string{}, commonPrefix...), q.InputArgs(Job{})...)
		dec = append(dec, "-hwaccel", "qsv", "-hwaccel_output_format", "qsv",
			"-i", "file:"+clipPath, "-vf", download, "-f", "null", "-")
		if _, err := run(ctx, env.runner, dec); err == nil {
			ok = append(ok, decodeKey(tc.codec, tc.depth))
		}
	}
	return ok
}

func run(ctx context.Context, r *ffmpeg.Runner, args []string) (ffmpeg.Result, error) {
	res, err := r.Run(ctx, args, testDuration, nil)
	if err != nil {
		return res, fmt.Errorf("%w: %s", err, strings.TrimSpace(res.StderrTail))
	}
	return res, nil
}

func probeFile(ctx context.Context, r *ffmpeg.Runner, path string) (*media.File, error) {
	probe, frames, err := r.Probe(ctx, path)
	if err != nil {
		return nil, err
	}
	f, err := media.Parse(probe, frames, 0)
	if err != nil {
		return nil, fmt.Errorf("reading the test output: %w", err)
	}
	return f, nil
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxDetail {
		return s[:maxDetail] + "..."
	}
	return s
}

func listOrNone(xs []string) string {
	if len(xs) == 0 {
		return "none"
	}
	return strings.Join(xs, ", ")
}
