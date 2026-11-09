//go:build !linux

package transcoder

// readProcessStats is Linux-only (/proc); other platforms report no sample so
// the FFmpeg panel shows N/A rather than fabricated values.
func readProcessStats(pid int) (uint64, int64, bool) {
	return 0, 0, false
}
