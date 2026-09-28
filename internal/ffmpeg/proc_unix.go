//go:build linux || darwin || freebsd || netbsd || openbsd

package ffmpeg

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// configure puts the process in its own process group, so that cancelling
// reaches anything it starts, and cancels with SIGTERM so ffmpeg can stop
// cleanly. exec.Cmd.WaitDelay then kills it if it does not exit.
func configure(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}

// lowerPriority lowers the scheduling priority of the process group led by
// pid. PRIO_PGRP reaches every thread on Linux, where nice values are
// per-thread and a process may have started threads before this runs.
// Errors are ignored: running at normal priority is not a reason to fail.
func lowerPriority(pid int) {
	_ = syscall.Setpriority(syscall.PRIO_PGRP, pid, niceness)
}
