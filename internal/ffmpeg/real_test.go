package ffmpeg

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/freakyturtle/jellytrim/internal/media"
	"github.com/freakyturtle/jellytrim/internal/testutil"
)

func realRunner(t *testing.T) *Runner {
	t.Helper()
	ff, fp := testutil.RequireFFmpeg(t)
	return New(ff, fp, nil)
}

func TestProbeRealFixture(t *testing.T) {
	r := realRunner(t)
	path := testutil.CopyFixture(t, "hevc-2160p-hdr10")
	probe, frames, err := r.Probe(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if frames == nil {
		t.Fatal("no frame probe")
	}
	f, err := media.Parse(probe, frames, 0)
	if err != nil {
		t.Fatal(err)
	}
	v, ok := f.MainVideo()
	if !ok || v.Codec != media.CodecHEVC || v.Width != 3840 || f.HDR().Class != media.HDR10 {
		t.Errorf("parsed %+v, class %s", v, f.HDR().Class)
	}
	if v.Mastering == nil || v.ContentLight == nil {
		t.Error("mastering display or content light missing from the frame probe")
	}
}

func TestProbeMissingFileIncludesStderr(t *testing.T) {
	r := realRunner(t)
	_, _, err := r.Probe(context.Background(), filepath.Join(t.TempDir(), "missing.mkv"))
	if err == nil || !strings.Contains(err.Error(), "No such file") {
		t.Errorf("err = %v", err)
	}
}

func TestPathsAreNotOptions(t *testing.T) {
	r := realRunner(t)
	// A file named like an option must be read as a file.
	dir := t.TempDir()
	src := testutil.CopyFixture(t, "h264-1080p")
	odd := filepath.Join(dir, "-version")
	if err := os.Rename(src, odd); err != nil {
		t.Fatal(err)
	}
	probe, _, err := r.Probe(context.Background(), odd)
	if err != nil || !strings.Contains(string(probe), `"codec_name": "h264"`) {
		t.Errorf("probe of %s: %v", odd, err)
	}
}

func TestRealRunAndDecode(t *testing.T) {
	r := realRunner(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "out.mkv")
	args := []string{
		"-nostdin", "-hide_banner", "-loglevel", "error", "-progress", "pipe:1", "-nostats", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=24:duration=2",
		"-c:v", "libx265", "-preset", "ultrafast", "-x265-params", "log-level=error",
		"-f", "matroska", "file:" + out,
	}
	var last Progress
	res, err := r.Run(context.Background(), args, 2*time.Second, func(p Progress) { last = p })
	if err != nil {
		t.Fatalf("run: %v\n%s", err, res.StderrTail)
	}
	if !last.Done || last.Fraction < 0.9 || last.TotalSize <= 0 {
		t.Errorf("last progress = %+v", last)
	}
	if err := r.Decode(context.Background(), out, false, 2*time.Second); err != nil {
		t.Errorf("decode: %v", err)
	}
	if err := r.Decode(context.Background(), out, true, 2*time.Second); err != nil {
		t.Errorf("sampled decode of a short file: %v", err)
	}

	// A truncated file fails the decode check.
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(dir, "broken.mkv")
	if err := os.WriteFile(broken, b[:len(b)/2], 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.Decode(context.Background(), broken, false, 2*time.Second); err == nil {
		t.Error("truncated file passed the decode check")
	}
}

func TestRealVersionAndEncoders(t *testing.T) {
	r := realRunner(t)
	v, err := r.Version(context.Background())
	if err != nil || !strings.HasPrefix(v, "ffmpeg version ") {
		t.Errorf("version %q: %v", v, err)
	}
	names, err := r.EncoderNames(context.Background())
	if err != nil || !names["libx265"] {
		t.Errorf("encoders: %v (libx265 present: %v)", err, names["libx265"])
	}
	help, err := r.EncoderHelp(context.Background(), "libx265")
	if err != nil || !strings.Contains(help, "-x265-params") {
		t.Errorf("help: %v", err)
	}
}
