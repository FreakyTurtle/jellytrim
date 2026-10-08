package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// Sampled decode checks three windows of this length.
const (
	decodeWindow = 20 * time.Second
	// A file shorter than this is decoded in full even when sampling:
	// the windows would cover most of it anyway.
	minSampledDuration = 3 * decodeWindow
)

// decodePositions are where the sampled windows start, as fractions of the
// duration.
var decodePositions = []float64{0.10, 0.50, 0.85}

// ErrDecode is returned when a decode check finds an error.
var ErrDecode = errors.New("decode check failed")

// DecodeArgs are the arguments for decoding the main video of path. start
// and length of zero decode the whole stream.
func DecodeArgs(path string, start, length time.Duration) []string {
	args := []string{"-nostdin", "-hide_banner", "-v", "error"}
	if start > 0 {
		args = append(args, "-ss", seconds(start))
	}
	args = append(args, "-i", "file:"+path)
	if length > 0 {
		args = append(args, "-t", seconds(length))
	}
	return append(args, "-map", "0:v:0", "-f", "null", "-")
}

// Decode checks that the main video of path decodes without errors (step 7
// of validation). Any line on stderr at error level fails the check, as
// does a non-zero exit. sampled decodes three 20-second windows at 10%, 50%
// and 85% of duration instead of the whole stream.
func (r *Runner) Decode(ctx context.Context, path string, sampled bool, duration time.Duration) error {
	if !sampled || duration < minSampledDuration {
		return r.decode(ctx, DecodeArgs(path, 0, 0))
	}
	for _, pos := range decodePositions {
		start := time.Duration(float64(duration) * pos)
		start = min(start, duration-decodeWindow)
		if err := r.decode(ctx, DecodeArgs(path, start, decodeWindow)); err != nil {
			return fmt.Errorf("at %s: %w", start.Round(time.Second), err)
		}
	}
	return nil
}

func (r *Runner) decode(ctx context.Context, args []string) error {
	stderr := newLineTail(stderrLines, stderrLineMax)
	cmd := r.command(ctx, r.FFmpeg, args)
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting ffmpeg: %w", err)
	}
	lowerPriority(cmd.Process.Pid)
	err := cmd.Wait()
	tail := stderr.String()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrDecode, commandError(ctx, "ffmpeg", err, tail))
	}
	if errs := ReportedErrors(tail); errs != "" {
		return fmt.Errorf("%w: %s", ErrDecode, lastLine(errs))
	}
	return nil
}

// seconds formats a duration as ffmpeg seconds with millisecond precision.
func seconds(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', 3, 64)
}
