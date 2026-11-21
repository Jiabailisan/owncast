//go:build !linux

package transcoder

import "errors"

// observeExit is only implemented on Linux. Elsewhere the caller keeps the Signal(0)
// probe as its guard, which leaves the window between that probe and the process
// group signal open.
func observeExit(int) error {
	return errors.New("exit observation is not supported on this platform")
}
