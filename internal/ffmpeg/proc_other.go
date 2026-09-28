//go:build !(linux || darwin || freebsd || netbsd || openbsd)

package ffmpeg

import (
	"os"
	"os/exec"
)

// configure cancels with an interrupt where the platform supports it;
// exec.Cmd.WaitDelay kills the process if it does not exit.
func configure(cmd *exec.Cmd) {
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
}

// lowerPriority is not supported on this platform.
func lowerPriority(int) {}
