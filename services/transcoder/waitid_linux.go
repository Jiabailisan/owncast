//go:build linux

package transcoder

import (
	"errors"

	"golang.org/x/sys/unix"
)

// observeExit waits for pid to exit without reaping it. WNOWAIT leaves the child
// waitable, so the process group identity stays valid until the single cmd.Wait
// reaps it, and Stop cannot signal a pid the kernel has already reused.
//
// It returns nil once the exit is observed, or an error when observation is not
// possible (the child is already gone, or the kernel refused the call), in which case
// the caller falls back to the Signal(0) probe as its only guard.
func observeExit(pid int) error {
	if pid <= 0 {
		return errors.New("invalid pid")
	}

	for {
		var info unix.Siginfo
		err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, unix.EINTR):
			continue
		default:
			return err
		}
	}
}
