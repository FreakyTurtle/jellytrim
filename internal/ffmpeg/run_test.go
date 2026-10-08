package ffmpeg

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// safeArgs is the minimum Run accepts.
var safeArgs = []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-progress", "pipe:1", "-nostats", "-y"}

func fakeRunner(t *testing.T, mode string) *Runner {
	t.Helper()
	t.Setenv(fakeEnv, mode)
	// A race-enabled binary sleeps for a second on a clean exit by default.
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	r := New(os.Args[0], os.Args[0], nil)
	return r
}

func TestRunParsesProgress(t *testing.T) {
	r := fakeRunner(t, "progress")
	var got []Progress
	res, err := r.Run(context.Background(), safeArgs, 10*time.Second, func(p Progress) { got = append(got, p) })
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []Progress{
		{},
		{OutTime: 2 * time.Second, Fraction: 0.2, Speed: 2, TotalSize: 250000},
		{OutTime: 4 * time.Second, Fraction: 0.4, Speed: 2.41, TotalSize: 500000},
		{OutTime: 10 * time.Second, Fraction: 1, Speed: 2.5, TotalSize: 1250000, Done: true},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d blocks, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("block %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if res.ExitCode != 0 || res.Last != want[3] {
		t.Errorf("result = code %d last %+v", res.ExitCode, res.Last)
	}
	if res.StderrTail != "a warning ffmpeg printed\n" {
		t.Errorf("stderr tail = %q", res.StderrTail)
	}
	if strings.Join(res.Args, " ") != strings.Join(safeArgs, " ") {
		t.Errorf("args = %q", res.Args)
	}
}

func TestRunUnknownTotalGivesZeroFraction(t *testing.T) {
	r := fakeRunner(t, "progress")
	res, err := r.Run(context.Background(), safeArgs, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Last.Fraction != 0 || res.Last.OutTime != 10*time.Second {
		t.Errorf("last = %+v", res.Last)
	}
}

func TestRunRefusesUnsafeArgs(t *testing.T) {
	r := fakeRunner(t, "quiet")
	cases := map[string][]string{
		"no progress":        {"-nostdin", "-y"},
		"progress elsewhere": {"-nostdin", "-progress", "file:x"},
		"no nostdin":         {"-progress", "pipe:1"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := r.Run(context.Background(), args, 0, nil); !errors.Is(err, ErrUnsafeArgs) {
				t.Errorf("err = %v, want ErrUnsafeArgs", err)
			}
		})
	}
}

func TestRunKeepsLast200StderrLines(t *testing.T) {
	r := fakeRunner(t, "fail")
	res, err := r.Run(context.Background(), safeArgs, 0, nil)
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 || res.ExitCode != 3 {
		t.Fatalf("err = %v, code %d", err, res.ExitCode)
	}
	lines := strings.Split(strings.TrimSuffix(res.StderrTail, "\n"), "\n")
	if len(lines) != 200 || lines[0] != "error line 101" || lines[199] != "error line 300" {
		t.Errorf("tail has %d lines, first %q last %q", len(lines), lines[0], lines[len(lines)-1])
	}
	if !strings.Contains(err.Error(), "error line 300") {
		t.Errorf("error %q does not name the last stderr line", err)
	}
}

func TestRunCancelStopsWithSIGTERM(t *testing.T) {
	r := fakeRunner(t, "hang")
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	var once bool
	go func() {
		<-started
		cancel()
	}()
	begin := time.Now()
	_, err := r.Run(ctx, safeArgs, 0, func(Progress) {
		if !once {
			once = true
			close(started)
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if d := time.Since(begin); d > 5*time.Second {
		t.Errorf("cancel took %s", d)
	}
}

func TestRunCancelKillsAfterDelay(t *testing.T) {
	r := fakeRunner(t, "ignore-term")
	r.KillDelay = 300 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	begin := time.Now()
	_, err := r.Run(ctx, safeArgs, 0, func(Progress) { cancel() })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if d := time.Since(begin); d > 5*time.Second {
		t.Errorf("kill took %s", d)
	}
}

func TestVersionFirstLine(t *testing.T) {
	r := fakeRunner(t, "version")
	v, err := r.Version(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v != "ffmpeg version 8.1-fake Copyright (c) 2000-2026 the FFmpeg developers" {
		t.Errorf("version = %q", v)
	}
}

func TestDecodeFailsOnStderr(t *testing.T) {
	r := fakeRunner(t, "decode-error")
	err := r.Decode(context.Background(), "/tmp/x.mkv", false, 0)
	if !errors.Is(err, ErrDecode) || !strings.Contains(err.Error(), "Could not find ref") {
		t.Errorf("err = %v", err)
	}
}

func TestDecodeIgnoresLibvaInfo(t *testing.T) {
	r := fakeRunner(t, "libva-info")
	if err := r.Decode(context.Background(), "/tmp/x.mkv", false, 0); err != nil {
		t.Errorf("err = %v", err)
	}
}

func TestRunnerAsksLibvaForErrorsOnly(t *testing.T) {
	r := fakeRunner(t, "libva-env")
	if err := r.Decode(context.Background(), "/tmp/x.mkv", false, 0); err != nil {
		t.Errorf("libva was not told to keep quiet: %v", err)
	}
}

func TestReportedErrors(t *testing.T) {
	cases := []struct{ name, tail, want string }{
		{"empty", "", ""},
		{"libva only", libvaInfo, ""},
		{"libva then an error", libvaInfo + "[hevc @ 0x0] Could not find ref with POC 12\n", "[hevc @ 0x0] Could not find ref with POC 12"},
		{"libva errors count", "libva error: vaGetDriverNames() failed with unknown libva error\n", "libva error: vaGetDriverNames() failed with unknown libva error"},
		{"blank lines", "\n  \r\n", ""},
	}
	for _, tc := range cases {
		if got := ReportedErrors(tc.tail); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestDecodeFailsOnExitCode(t *testing.T) {
	r := fakeRunner(t, "fail")
	if err := r.Decode(context.Background(), "/tmp/x.mkv", false, 0); !errors.Is(err, ErrDecode) {
		t.Errorf("err = %v", err)
	}
}

func TestDecodeSampledWindows(t *testing.T) {
	r := fakeRunner(t, "quiet")
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv(fakeArgsEnv, argsFile)
	if err := r.Decode(context.Background(), "/media/a b.mkv", true, 100*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := r.Decode(context.Background(), "/media/short.mkv", true, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"-nostdin -hide_banner -v error -ss 10.000 -i file:/media/a b.mkv -t 20.000 -map 0:v:0 -f null -",
		"-nostdin -hide_banner -v error -ss 50.000 -i file:/media/a b.mkv -t 20.000 -map 0:v:0 -f null -",
		"-nostdin -hide_banner -v error -ss 80.000 -i file:/media/a b.mkv -t 20.000 -map 0:v:0 -f null -",
		"-nostdin -hide_banner -v error -i file:/media/short.mkv -map 0:v:0 -f null -",
	}, "\n") + "\n"
	if string(b) != want {
		t.Errorf("runs:\n%s\nwant:\n%s", b, want)
	}
}

func TestProgressParserIgnoresNoise(t *testing.T) {
	p := progressParser{total: 4 * time.Second}
	for _, l := range []string{"", "garbage", "out_time_us=-5000", "speed=N/A", "total_size=N/A"} {
		if _, ok := p.feed(l); ok {
			t.Fatalf("%q ended a block", l)
		}
	}
	got, ok := p.feed("progress=continue")
	if !ok || got != (Progress{}) {
		t.Errorf("got %+v %v", got, ok)
	}
}

func TestParseEncoders(t *testing.T) {
	out := []byte(`Encoders:
 V..... = Video
 A..... = Audio
 ------
 V....D libx265              libx265 H.265 / HEVC (codec hevc)
 V..... hevc_qsv             HEVC (Intel Quick Sync Video acceleration) (codec hevc)
 A....D aac                  AAC (Advanced Audio Coding)
`)
	got := parseEncoders(out)
	for _, name := range []string{"libx265", "hevc_qsv", "aac"} {
		if !got[name] {
			t.Errorf("%s missing from %v", name, got)
		}
	}
	if got["V....."] || got["="] || len(got) != 3 {
		t.Errorf("legend parsed as encoders: %v", got)
	}
}

func TestLineTailCapsLineLength(t *testing.T) {
	tail := newLineTail(2, 5)
	_, _ = tail.Write([]byte("abcdefgh\nsecond\r\nthi"))
	_, _ = tail.Write([]byte("rd\n"))
	if got := tail.String(); got != "secon\nthird\n" {
		t.Errorf("tail = %q", got)
	}
}
