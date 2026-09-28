package ffmpeg

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The test binary doubles as a fake ffmpeg: when JELLYTRIM_FAKE_FFMPEG is
// set, TestMain runs the named behaviour instead of the tests. Runner.FFmpeg
// is set to os.Args[0] and the child inherits the variable.
const fakeEnv = "JELLYTRIM_FAKE_FFMPEG"

// fakeArgsEnv names a file the fake appends its arguments to, one line per
// run, so tests can check what was run.
const fakeArgsEnv = "JELLYTRIM_FAKE_ARGS"

func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeEnv); mode != "" {
		os.Exit(fakeFFmpeg(mode, os.Args[1:]))
	}
	os.Exit(m.Run())
}

func fakeFFmpeg(mode string, args []string) int {
	if path := os.Getenv(fakeArgsEnv); path != "" {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = fmt.Fprintln(f, strings.Join(args, " "))
			_ = f.Close()
		}
	}
	switch mode {
	case "progress":
		fakeProgress()
		return 0
	case "fail":
		for i := 1; i <= 300; i++ {
			fmt.Fprintf(os.Stderr, "error line %d\n", i)
		}
		return 3
	case "hang":
		fmt.Println("out_time_us=1000000\nspeed=1x\ntotal_size=10\nprogress=continue")
		time.Sleep(30 * time.Second)
		return 0
	case "ignore-term":
		signal.Ignore(syscall.SIGTERM)
		fmt.Println("progress=continue")
		time.Sleep(30 * time.Second)
		return 0
	case "quiet":
		return 0
	case "decode-error":
		fmt.Fprintln(os.Stderr, "[hevc @ 0x0] Could not find ref with POC 12")
		return 0
	case "version":
		fmt.Println("ffmpeg version 8.1-fake Copyright (c) 2000-2026 the FFmpeg developers\nbuilt with clang")
		return 0
	}
	fmt.Fprintln(os.Stderr, "unknown fake mode", mode)
	return 2
}

// fakeProgress writes what ffmpeg 8 writes with -progress pipe:1, including
// the N/A values of the first block and padded speeds.
func fakeProgress() {
	fmt.Fprintln(os.Stderr, "a warning ffmpeg printed")
	blocks := []string{
		"frame=0\nfps=0.00\nbitrate=N/A\ntotal_size=N/A\nout_time_us=N/A\nout_time=N/A\nspeed=N/A\nprogress=continue",
		"frame=48\nfps=24.00\nbitrate=1000.0kbits/s\ntotal_size=250000\nout_time_us=2000000\nout_time=00:00:02.000000\nspeed=   2x\nprogress=continue",
		"frame=96\ntotal_size=500000\nout_time_us=4000000\nspeed=2.41x\nprogress=continue",
		"frame=240\ntotal_size=1250000\nout_time_us=10000000\nspeed=2.5x\nprogress=end",
	}
	for _, b := range blocks {
		fmt.Println(b)
	}
}
