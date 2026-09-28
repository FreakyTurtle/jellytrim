// Package ffmpeg is the only package in JellyTrim that runs processes. It
// runs ffmpeg and ffprobe with argument slices (never a shell), parses
// progress, keeps the tail of stderr for diagnostics and makes sure a
// cancelled job stops. See docs/TRANSCODING.md sections 1, 5 and 6.
package ffmpeg

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// Defaults for Runner.
const (
	defaultKillDelay = 10 * time.Second
	shortTimeout     = 30 * time.Second
	// niceness is the scheduling priority encodes and decode checks run
	// at, so the server stays responsive while x265 uses every core.
	niceness = 10
)

// ErrUnsafeArgs is returned by Run when the arguments lack the options
// JellyTrim relies on to read progress and keep ffmpeg off the terminal.
var ErrUnsafeArgs = errors.New("ffmpeg arguments must include -nostdin and -progress pipe:1")

// Runner runs ffmpeg and ffprobe.
type Runner struct {
	FFmpeg  string // path or name of the ffmpeg binary
	FFprobe string // path or name of the ffprobe binary
	Log     *slog.Logger
	// KillDelay is how long a cancelled process gets to exit after SIGTERM
	// before it is killed. Zero means 10 seconds.
	KillDelay time.Duration
}

// New returns a Runner. Empty paths default to "ffmpeg" and "ffprobe" on
// PATH; a nil logger discards logs.
func New(ffmpegPath, ffprobePath string, log *slog.Logger) *Runner {
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	if ffprobePath == "" {
		ffprobePath = "ffprobe"
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Runner{FFmpeg: ffmpegPath, FFprobe: ffprobePath, Log: log}
}

func (r *Runner) logger() *slog.Logger {
	if r.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return r.Log
}

func (r *Runner) killDelay() time.Duration {
	if r.KillDelay > 0 {
		return r.KillDelay
	}
	return defaultKillDelay
}

// command builds a process that is cancelled with SIGTERM (then killed after
// KillDelay) and runs in its own process group.
func (r *Runner) command(ctx context.Context, bin string, args []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, bin, args...)
	configure(cmd)
	cmd.WaitDelay = r.killDelay()
	return cmd
}

// Version returns the first line of `ffmpeg -version`, for example
// "ffmpeg version 8.1.2 Copyright (c) 2000-2026 the FFmpeg developers".
func (r *Runner) Version(ctx context.Context) (string, error) {
	out, _, err := r.output(ctx, r.FFmpeg, []string{"-hide_banner", "-version"}, shortTimeout)
	if err != nil {
		return "", fmt.Errorf("running ffmpeg -version: %w", err)
	}
	line, _, _ := strings.Cut(string(out), "\n")
	return strings.TrimSpace(line), nil
}

// EncoderNames returns the encoders ffmpeg was built with, keyed by name
// (for example "libx265", "hevc_qsv").
func (r *Runner) EncoderNames(ctx context.Context) (map[string]bool, error) {
	out, _, err := r.output(ctx, r.FFmpeg, []string{"-hide_banner", "-encoders"}, shortTimeout)
	if err != nil {
		return nil, fmt.Errorf("listing ffmpeg encoders: %w", err)
	}
	return parseEncoders(out), nil
}

// EncoderHelp returns `ffmpeg -h encoder=<name>`, which lists the
// encoder's private options.
func (r *Runner) EncoderHelp(ctx context.Context, name string) (string, error) {
	out, _, err := r.output(ctx, r.FFmpeg, []string{"-hide_banner", "-h", "encoder=" + name}, shortTimeout)
	if err != nil {
		return "", fmt.Errorf("reading ffmpeg help for %s: %w", name, err)
	}
	return string(out), nil
}

// parseEncoders reads the table after the "------" separator: a six-letter
// capability column, the encoder name and a description.
func parseEncoders(out []byte) map[string]bool {
	names := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	table := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !table {
			table = strings.HasPrefix(line, "------")
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || len(fields[0]) != 6 {
			continue
		}
		names[fields[1]] = true
	}
	return names
}

// output runs a short command and returns its stdout. A non-zero exit is an
// error that includes the tail of stderr.
func (r *Runner) output(ctx context.Context, bin string, args []string, timeout time.Duration) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var stdout bytes.Buffer
	stderr := newLineTail(20, 512)
	cmd := r.command(ctx, bin, args)
	cmd.Stdout = &stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	tail := stderr.String()
	if err != nil {
		return nil, tail, commandError(ctx, bin, err, tail)
	}
	return stdout.Bytes(), tail, nil
}

// commandError describes a failed run, preferring the context's reason.
func commandError(ctx context.Context, bin string, err error, tail string) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%s stopped: %w", bin, ctxErr)
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return &ExitError{Code: exitErr.ExitCode(), StderrTail: tail}
	}
	return fmt.Errorf("running %s: %w", bin, err)
}

// ExitError is a process that ran and exited with a non-zero code.
type ExitError struct {
	Code       int
	StderrTail string
}

func (e *ExitError) Error() string {
	last := lastLine(e.StderrTail)
	if last == "" {
		return fmt.Sprintf("exited with code %d", e.Code)
	}
	return fmt.Sprintf("exited with code %d: %s", e.Code, last)
}

func lastLine(s string) string {
	s = strings.TrimRight(s, "\n")
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// lineTail is an io.Writer that keeps the last max lines written to it,
// each cut to maxLen bytes, so a chatty process cannot use unbounded memory.
type lineTail struct {
	max, maxLen int
	lines       []string
	partial     []byte
}

func newLineTail(maxLines, maxLen int) *lineTail {
	return &lineTail{max: maxLines, maxLen: maxLen}
}

// Write implements io.Writer. It is not safe for concurrent use; os/exec
// writes from one goroutine and the caller reads after Wait.
func (t *lineTail) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexAny(p, "\r\n")
		if i < 0 {
			t.appendPartial(p)
			break
		}
		t.appendPartial(p[:i])
		t.flush()
		p = p[i+1:]
	}
	return n, nil
}

func (t *lineTail) appendPartial(b []byte) {
	if room := t.maxLen - len(t.partial); room > 0 {
		t.partial = append(t.partial, b[:min(len(b), room)]...)
	}
}

func (t *lineTail) flush() {
	if len(t.partial) == 0 {
		return
	}
	t.lines = append(t.lines, string(t.partial))
	t.partial = t.partial[:0]
	if len(t.lines) > 2*t.max {
		t.lines = append(t.lines[:0:0], t.lines[len(t.lines)-t.max:]...)
	}
}

// String returns the kept lines, newline-terminated.
func (t *lineTail) String() string {
	t.flush()
	lines := t.lines
	if len(lines) > t.max {
		lines = lines[len(lines)-t.max:]
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

var _ io.Writer = (*lineTail)(nil)
