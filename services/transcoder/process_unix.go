//go:build !windows

package transcoder

import (
	"os/exec"
	"syscall"
)

// configureProcessGroup places the ffmpeg child in its own process group so a
// stop can terminate the child and anything it spawned (spec 3.4/10: never
// leave orphaned ffmpeg processes).
func configureProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcessGroup terminates the child's process group. It falls back to
// killing the single process when the group is gone.
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return nil
	}
	// Probe through os.Process first: once Wait has reaped the child, Signal reports
	// os.ErrProcessDone and the raw group kill below is skipped. Without the probe a
	// late Stop could signal a pid the kernel has already reused.
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		return nil
	}
	// Negative pid signals the whole process group created by Setpgid.
	if err := syscall.Kill(-pid, syscall.SIGKILL); err == nil {
		return nil
	}
	return cmd.Process.Kill()
}
