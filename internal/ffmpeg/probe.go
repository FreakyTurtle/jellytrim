package ffmpeg

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// probeTimeout bounds each ffprobe run. A healthy file probes in well under
// a second; a stalled network mount must not hold a worker forever.
const probeTimeout = 2 * time.Minute

// ProbeArgs are the ffprobe arguments for streams, format and chapters
// (docs/TRANSCODING.md section 1).
func ProbeArgs(path string) []string {
	return []string{"-v", "error", "-show_format", "-show_streams", "-show_chapters", "-of", "json", "file:" + path}
}

// FrameProbeArgs are the ffprobe arguments for the first video frame's
// colour description and side data.
func FrameProbeArgs(path string) []string {
	return []string{
		"-v", "error", "-select_streams", "v:0", "-read_intervals", "%+#1", "-show_frames",
		"-show_entries", "frame=color_transfer,color_primaries,color_space,side_data_list",
		"-of", "json", "file:" + path,
	}
}

// Probe runs both ffprobe invocations. The stream probe must succeed; a
// failed frame probe is logged and returns nil frames, which the media
// model treats as "HDR10+ cannot be ruled out" for PQ content.
func (r *Runner) Probe(ctx context.Context, path string) (probeJSON, frameJSON []byte, err error) {
	probeJSON, tail, err := r.output(ctx, r.FFprobe, ProbeArgs(path), probeTimeout)
	if err != nil {
		return nil, nil, fmt.Errorf("ffprobe %s: %w%s", path, err, stderrSuffix(tail))
	}
	frameJSON, tail, err = r.output(ctx, r.FFprobe, FrameProbeArgs(path), probeTimeout)
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, fmt.Errorf("ffprobe frames %s: %w", path, err)
		}
		r.logger().Warn("ffmpeg: first-frame probe failed", "path", path, "err", err, "stderr", capTail(tail))
		return probeJSON, nil, nil
	}
	return probeJSON, frameJSON, nil
}

// maxErrTail caps how much stderr goes into an error message.
const maxErrTail = 1024

// capTail keeps the end of s, at most maxErrTail bytes.
func capTail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxErrTail {
		s = "..." + s[len(s)-maxErrTail:]
	}
	return s
}

func stderrSuffix(tail string) string {
	if t := capTail(tail); t != "" {
		return " (stderr: " + t + ")"
	}
	return ""
}
