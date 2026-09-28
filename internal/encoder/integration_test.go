package encoder

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/freakyturtle/jellytrim/internal/ffmpeg"
	"github.com/freakyturtle/jellytrim/internal/media"
	"github.com/freakyturtle/jellytrim/internal/plan"
	"github.com/freakyturtle/jellytrim/internal/policy"
	"github.com/freakyturtle/jellytrim/internal/testutil"
)

func realRunner(t *testing.T) *ffmpeg.Runner {
	t.Helper()
	ff, fp := testutil.RequireFFmpeg(t)
	return ffmpeg.New(ff, fp, nil)
}

// probeReal probes a real file into the media model.
func probeReal(t *testing.T, r *ffmpeg.Runner, path string) *media.File {
	t.Helper()
	probe, frames, err := r.Probe(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := media.Parse(probe, frames, 0)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func hashFile(t *testing.T, path string) [32]byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(b)
}

// TestRealEncodes runs x265 jobs on copies of the fixtures and checks the
// outputs the way validation will: codec, size, colour, every stream kept
// with its language, title and flags, and the JellyTrim tag.
func TestRealEncodes(t *testing.T) {
	r := realRunner(t)
	cases := []struct {
		fixture string
		w, h    int
	}{
		{"h264-1080p", 0, 0},
		{"hevc-2160p-hdr10", 1920, 1080},
		{"hevc-1080p-hlg", 1280, 720},
		{"multi-audio-subs", 0, 0},
		{"anime-ass", 0, 0},
		{"mp4-movtext-cover", 1280, 720},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			in := testutil.CopyFixture(t, tc.fixture)
			before := hashFile(t, in)
			src := probeReal(t, r, in)
			p := planFor(t, src, tc.w, tc.h, policy.QualityHigh)
			j := Job{Plan: p, Source: src, Input: in, Output: in + ".partial", JobID: 1, Overrides: Overrides{Preset: "medium"}}
			args, err := BuildArgs(hdrX265(), j)
			if err != nil {
				t.Fatal(err)
			}
			res, err := r.Run(context.Background(), args, src.Duration, nil)
			if err != nil {
				t.Fatalf("encode: %v\n%s", err, res.StderrTail)
			}
			if hashFile(t, in) != before {
				t.Fatal("the source changed")
			}
			out := probeReal(t, r, j.Output)
			checkOutput(t, src, out, p)
			if err := r.Decode(context.Background(), j.Output, false, out.Duration); err != nil {
				t.Errorf("decode check: %v", err)
			}
			raw, _ := os.ReadFile(j.Output)
			if !bytes.Contains(raw, []byte("crf=21.0")) {
				t.Error("x265's settings SEI does not show crf=21.0; the scoped -crf:v:0 was not applied")
			}
		})
	}
}

func checkOutput(t *testing.T, src, out *media.File, p plan.Plan) {
	t.Helper()
	v, _ := out.MainVideo()
	if v.Codec != media.CodecHEVC || v.Width != p.Width || v.Height != p.Height || v.BitDepth != 10 {
		t.Errorf("video is %s %dx%d %d-bit, want HEVC %dx%d 10-bit", v.Codec, v.Width, v.Height, v.BitDepth, p.Width, p.Height)
	}
	if sv, _ := src.MainVideo(); v.Disposition != sv.Disposition {
		t.Errorf("video disposition %+v, source %+v", v.Disposition, sv.Disposition)
	}
	if v.SAR != "" && v.SAR != "1:1" {
		t.Errorf("sample aspect ratio became %s", v.SAR)
	}
	// media.Parse reports the container of a ".partial" file as other, so
	// compare ffprobe's format name.
	if out.FormatName != src.FormatName || out.Chapters != src.Chapters {
		t.Errorf("format %s chapters %d, want %s %d", out.FormatName, out.Chapters, src.FormatName, src.Chapters)
	}
	wantTag := "v1;enc=x265;q=high"
	if src.Container == media.MP4 {
		wantTag = "" // see metadataArgs
	}
	if out.JellyTrimTag() != wantTag {
		t.Errorf("JELLYTRIM tag = %q, want %q", out.JellyTrimTag(), wantTag)
	}
	if d := out.Duration - src.Duration; d > 200*time.Millisecond || d < -200*time.Millisecond {
		t.Errorf("duration %s, source %s", out.Duration, src.Duration)
	}
	checkColour(t, src, out)
	checkStreams(t, src, out)
}

func checkColour(t *testing.T, src, out *media.File) {
	t.Helper()
	sv, _ := src.MainVideo()
	ov, _ := out.MainVideo()
	if out.HDR().Class != src.HDR().Class {
		t.Errorf("HDR class %s, source %s (facts %v)", out.HDR().Class, src.HDR().Class, out.HDR().Facts)
	}
	if ov.Primaries != sv.Primaries || ov.Transfer != sv.Transfer || ov.Matrix != sv.Matrix {
		t.Errorf("colour %q/%q/%q, source %q/%q/%q", ov.Primaries, ov.Transfer, ov.Matrix, sv.Primaries, sv.Transfer, sv.Matrix)
	}
	if (sv.Mastering == nil) != (ov.Mastering == nil) || sv.Mastering != nil && *sv.Mastering != *ov.Mastering {
		t.Errorf("mastering display %+v, source %+v", ov.Mastering, sv.Mastering)
	}
	if (sv.ContentLight == nil) != (ov.ContentLight == nil) || sv.ContentLight != nil && *sv.ContentLight != *ov.ContentLight {
		t.Errorf("content light %+v, source %+v", ov.ContentLight, sv.ContentLight)
	}
}

func checkStreams(t *testing.T, src, out *media.File) {
	t.Helper()
	if len(out.Audio) != len(src.Audio) || len(out.Subtitles) != len(src.Subtitles) ||
		len(out.Attachments) != len(src.Attachments) || len(out.CoverArt()) != len(src.CoverArt()) {
		t.Fatalf("streams: audio %d/%d subs %d/%d attachments %d/%d cover %d/%d",
			len(out.Audio), len(src.Audio), len(out.Subtitles), len(src.Subtitles),
			len(out.Attachments), len(src.Attachments), len(out.CoverArt()), len(src.CoverArt()))
	}
	for i, a := range src.Audio {
		o := out.Audio[i]
		if o.Codec != a.Codec || o.Language != a.Language || o.Title != a.Title || o.Disposition != a.Disposition {
			t.Errorf("audio %d: %+v, source %+v", i, o, a)
		}
	}
	for i, s := range src.Subtitles {
		o := out.Subtitles[i]
		if o.Codec != s.Codec || o.Language != s.Language || o.Title != s.Title || o.Disposition != s.Disposition {
			t.Errorf("subtitle %d: %+v, source %+v", i, o, s)
		}
	}
	for i, c := range src.CoverArt() {
		if o := out.CoverArt()[i]; o.Codec != c.Codec || o.Width != c.Width {
			t.Errorf("cover art %d: %s %dx%d, source %s %dx%d", i, o.Codec, o.Width, o.Height, c.Codec, c.Width, c.Height)
		}
	}
	for i, a := range src.Attachments {
		if o := out.Attachments[i]; o.FileName != a.FileName || o.MimeType != a.MimeType {
			t.Errorf("attachment %d: %+v, source %+v", i, o, a)
		}
	}
}

// TestDetectOnThisMachine runs the real hardware probe. x265 must pass with
// HDR passthrough; QSV must report a missing device without running ffmpeg
// when there is no /dev/dri.
func TestDetectOnThisMachine(t *testing.T) {
	r := realRunner(t)
	t.Setenv("TMPDIR", t.TempDir())
	reg := NewRegistry(DefaultBackends(""))
	got := reg.Detect(context.Background(), r)
	byName := map[string]Capability{}
	for _, c := range got {
		byName[c.Backend] = c
	}
	x := byName[NameX265]
	if !x.Available || !x.HDRPassthrough || !x.DolbyVisionOption || x.Error != "" {
		t.Errorf("x265 = %+v", x)
	}
	if !strings.HasPrefix(x.FFmpegVersion, "ffmpeg version") || x.TestedAt.IsZero() {
		t.Errorf("x265 version %q tested %v", x.FFmpegVersion, x.TestedAt)
	}
	if _, err := os.Stat(DefaultQSVDevice); err != nil {
		q := byName[NameQSV]
		if q.Available || q.Error != "No Intel GPU device (/dev/dri/renderD128) found." || q.Detail != "" {
			t.Errorf("qsv = %+v", q)
		}
	}
	if name, ok, _ := reg.Select(media.CodecHEVC, true, Auto); !ok || name != NameX265 {
		t.Errorf("HDR selection after detection = %q %v", name, ok)
	}
	entries, _ := os.ReadDir(os.Getenv("TMPDIR"))
	if len(entries) != 0 {
		t.Errorf("detection left %d temporary entries", len(entries))
	}
}

func TestDetectReportsMissingEncoder(t *testing.T) {
	r := realRunner(t)
	c := detectBackend(context.Background(), detectEnv{runner: r, encoders: map[string]bool{}}, X265{})
	if c.Available || c.Error != "This ffmpeg does not include the libx265 encoder." {
		t.Errorf("capability = %+v", c)
	}
}

func TestSampleEstimatesSize(t *testing.T) {
	r := realRunner(t)
	t.Setenv("TMPDIR", t.TempDir())
	in := testutil.CopyFixture(t, "h264-1080p")
	src := probeReal(t, r, in)
	p := planFor(t, src, 0, 0, policy.QualityHigh)
	j := Job{Plan: p, Source: src, Input: in, Output: in + ".partial", Overrides: Overrides{Preset: "medium"}}
	est, err := Sample(context.Background(), r, hdrX265(), j, 3)
	if err != nil {
		t.Fatal(err)
	}
	// A 3-second clip is sampled whole, so the estimate is close to a real
	// encode: well under the 8 Mbps source, and more than the audio.
	if est <= copiedBytes(j) || est >= src.Size {
		t.Errorf("estimate %d bytes, source %d, copied streams %d", est, src.Size, copiedBytes(j))
	}
	if _, err := os.Stat(j.Output); !os.IsNotExist(err) {
		t.Error("sampling wrote the job's output file")
	}
}

func TestSampleWindows(t *testing.T) {
	cases := []struct {
		d    time.Duration
		n    int
		want []window
	}{
		{30 * time.Second, 3, []window{{0, 30 * time.Second}}},
		{100 * time.Minute, 3, []window{
			{1000*time.Second - 10*time.Second, sampleLength},
			{3000*time.Second - 10*time.Second, sampleLength},
			{5000*time.Second - 10*time.Second, sampleLength},
		}},
		{70 * time.Second, 3, []window{{1666 * time.Millisecond, sampleLength}, {25 * time.Second, sampleLength}, {48333 * time.Millisecond, sampleLength}}},
		{2 * time.Minute, 0, []window{{50 * time.Second, sampleLength}}},
	}
	for _, tc := range cases {
		got := sampleWindows(tc.d, tc.n)
		if len(got) != len(tc.want) {
			t.Errorf("%s/%d: %v", tc.d, tc.n, got)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s/%d window %d = %v, want %v", tc.d, tc.n, i, got[i], tc.want[i])
			}
		}
	}
}

func TestSegmentArgs(t *testing.T) {
	f := loadFixture(t, "multi-audio-subs")
	j := jobFor(f, planFor(t, f, 0, 0, policy.QualityHigh))
	j.Output = "/tmp/seg.mkv"
	args, err := build(hdrX265(), j, buildMode{segment: true, start: 90 * time.Second, length: sampleLength})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"-ss 90.000 -i file:", "-map 0:0 -map_metadata -1 -map_chapters -1 -an -sn -dn -c:v:0 libx265", "-t 20.000 -f matroska file:/tmp/seg.mkv"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %s", want, joined)
		}
	}
	if strings.Contains(joined, "JELLYTRIM") || strings.Contains(joined, "-map 0:1") {
		t.Errorf("segment maps or tags more than the video: %s", joined)
	}
}
