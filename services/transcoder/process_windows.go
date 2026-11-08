//go:build windows

package transcoder

import "os/exec"

// configureProcessGroup is a no-op on Windows: Owncast spawns ffmpeg.exe
// directly and there is no POSIX process group to terminate.
func configureProcessGroup(cmd *exec.Cmd) {}

// killProcessGroup terminates the child process on Windows.
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
