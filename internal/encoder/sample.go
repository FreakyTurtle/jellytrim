package encoder

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/freakyturtle/jellytrim/internal/ffmpeg"
)

// sampleLength is the length of each sampled segment.
const sampleLength = 20 * time.Second

// window is one sampled segment.
type window struct {
	start, length time.Duration
}

// sampleWindows spreads n segments evenly through the file, centred on
// (2i+1)/2n of the duration. A file too short for n separate segments is
// encoded whole.
func sampleWindows(d time.Duration, n int) []window {
	n = max(n, 1)
	if d <= time.Duration(n)*sampleLength {
		return []window{{0, d}}
	}
	ws := make([]window, 0, n)
	for i := range n {
		centre := time.Duration(float64(d) * float64(2*i+1) / float64(2*n))
		start := min(max(centre-sampleLength/2, 0), d-sampleLength)
		ws = append(ws, window{start.Truncate(time.Millisecond), sampleLength})
	}
	return ws
}

// Sample estimates the output size by encoding n evenly spaced 20-second
// segments of the main video with the job's settings, scaling their size to
// the full duration and adding the streams that are copied unchanged. This
// is the hook quality-based (VMAF) selection will build on.
func Sample(ctx context.Context, runner *ffmpeg.Runner, b Backend, j Job, n int) (int64, error) {
	if j.Source == nil || j.Source.Duration <= 0 {
		return 0, errors.New("sampling needs the source's duration")
	}
	dir, err := os.MkdirTemp("", "jellytrim-sample-*")
	if err != nil {
		return 0, fmt.Errorf("sample folder: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	var bytes int64
	var covered time.Duration
	for i, w := range sampleWindows(j.Source.Duration, n) {
		sj := j
		sj.Output = filepath.Join(dir, fmt.Sprintf("segment-%d.mkv", i))
		args, err := build(b, sj, buildMode{segment: true, start: w.start, length: w.length})
		if err != nil {
			return 0, err
		}
		if res, err := runner.Run(ctx, args, w.length, nil); err != nil {
			return 0, fmt.Errorf("sample segment at %s: %w (%s)", w.start, err, clip(res.StderrTail))
		}
		fi, err := os.Stat(sj.Output)
		if err != nil {
			return 0, fmt.Errorf("sample segment at %s: %w", w.start, err)
		}
		bytes += fi.Size()
		covered += w.length
	}
	video := float64(bytes) / covered.Seconds() * j.Source.Duration.Seconds()
	return int64(video) + copiedBytes(j), nil
}

// copiedBytes estimates the streams copied unchanged: the source size
// minus its video, or the audio bitrates when the video bitrate is unknown.
func copiedBytes(j Job) int64 {
	f := j.Source
	size := j.Plan.SourceSize
	if size <= 0 {
		size = f.Size
	}
	secs := f.Duration.Seconds()
	if bps, ok := f.VideoBitrate(); ok && size > 0 {
		if rest := size - int64(float64(bps)*secs/8); rest > 0 {
			return rest
		}
	}
	var audio int64
	for _, a := range f.Audio {
		audio += a.BitRate
	}
	return int64(float64(audio) * secs / 8)
}
