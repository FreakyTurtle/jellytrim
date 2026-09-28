package ffmpeg

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"
)

// stderrLines is how much of ffmpeg's stderr a job keeps for diagnostics.
const (
	stderrLines   = 200
	stderrLineMax = 2048
)

// Progress is one -progress block.
type Progress struct {
	OutTime   time.Duration // position in the output
	Fraction  float64       // OutTime / total, 0 to 1; 0 when total is unknown
	Speed     float64       // multiple of real time; 0 when not yet known
	TotalSize int64         // bytes written so far
	Done      bool          // ffmpeg reported progress=end
}

// Result describes a finished ffmpeg run.
type Result struct {
	ExitCode   int
	StderrTail string
	Args       []string
	Duration   time.Duration
	Last       Progress // the last progress block
}

// Run runs ffmpeg with args and reports progress. The caller supplies every
// argument; Run refuses arguments without -nostdin and -progress pipe:1.
// total is the expected output duration, used for Progress.Fraction.
// onProgress may be nil. A non-zero exit returns *ExitError; cancellation
// returns an error wrapping the context's error.
func (r *Runner) Run(ctx context.Context, args []string, total time.Duration, onProgress func(Progress)) (Result, error) {
	res := Result{Args: slices.Clone(args), ExitCode: -1}
	if !hasPair(args, "-progress", "pipe:1") || !slices.Contains(args, "-nostdin") {
		return res, ErrUnsafeArgs
	}
	stderr := newLineTail(stderrLines, stderrLineMax)
	cmd := r.command(ctx, r.FFmpeg, args)
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return res, fmt.Errorf("ffmpeg stdout: %w", err)
	}
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return res, fmt.Errorf("starting ffmpeg: %w", err)
	}
	lowerPriority(cmd.Process.Pid)
	r.logger().Debug("ffmpeg: started", "pid", cmd.Process.Pid)

	res.Last = readProgress(stdout, total, onProgress)
	waitErr := cmd.Wait()
	res.Duration = time.Since(start)
	res.StderrTail = stderr.String()
	res.ExitCode = cmd.ProcessState.ExitCode()
	if waitErr != nil {
		return res, runError(ctx, waitErr, res)
	}
	return res, nil
}

func runError(ctx context.Context, waitErr error, res Result) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("ffmpeg stopped: %w", ctxErr)
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		return &ExitError{Code: res.ExitCode, StderrTail: res.StderrTail}
	}
	return fmt.Errorf("waiting for ffmpeg: %w", waitErr)
}

func hasPair(args []string, key, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == key && args[i+1] == value {
			return true
		}
	}
	return false
}

// readProgress reads key=value lines until EOF, reporting each block.
func readProgress(rd io.Reader, total time.Duration, onProgress func(Progress)) Progress {
	p := progressParser{total: total}
	sc := bufio.NewScanner(rd)
	sc.Buffer(make([]byte, 0, 4096), 64*1024)
	for sc.Scan() {
		if block, ok := p.feed(sc.Text()); ok && onProgress != nil {
			onProgress(block)
		}
	}
	// Drain anything left (an over-long line stops the scanner) so ffmpeg
	// never blocks writing to a full pipe.
	_, _ = io.Copy(io.Discard, rd)
	return p.cur
}

// progressParser accumulates -progress key=value lines. Values reported as
// N/A keep the previous value.
type progressParser struct {
	total time.Duration
	cur   Progress
}

// feed reads one line and returns a finished block when the line is the
// block's closing "progress=" key.
func (p *progressParser) feed(line string) (Progress, bool) {
	key, val, ok := strings.Cut(strings.TrimSpace(line), "=")
	if !ok {
		return Progress{}, false
	}
	val = strings.TrimSpace(val)
	switch key {
	case "out_time_us":
		if us, err := strconv.ParseInt(val, 10, 64); err == nil {
			p.cur.OutTime = max(time.Duration(us)*time.Microsecond, 0)
		}
	case "speed":
		if s, err := strconv.ParseFloat(strings.TrimSuffix(val, "x"), 64); err == nil && s >= 0 {
			p.cur.Speed = s
		}
	case "total_size":
		if n, err := strconv.ParseInt(val, 10, 64); err == nil && n >= 0 {
			p.cur.TotalSize = n
		}
	case "progress":
		p.cur.Done = val == "end"
		p.cur.Fraction = fraction(p.cur.OutTime, p.total)
		return p.cur, true
	}
	return Progress{}, false
}

func fraction(done, total time.Duration) float64 {
	if total <= 0 {
		return 0
	}
	return min(max(float64(done)/float64(total), 0), 1)
}
