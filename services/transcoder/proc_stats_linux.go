//go:build linux

package transcoder

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// readProcessStats reads utime+stime (CPU jiffies) and VmRSS (bytes) for pid
// from /proc. It is Linux-only; other platforms report no sample (N/A).
func readProcessStats(pid int) (uint64, int64, bool) {
	statBytes, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, 0, false
	}
	stat := string(statBytes)
	// The comm field can contain spaces and parentheses; everything after the
	// last ')' is fixed: state, ppid, pgrp, session, tty_nr, tpgid, flags,
	// minflt, cminflt, majflt, cmajflt, utime, stime, ...
	closeParen := strings.LastIndex(stat, ")")
	if closeParen < 0 || closeParen+2 >= len(stat) {
		return 0, 0, false
	}
	fields := strings.Fields(stat[closeParen+2:])
	// fields[0] is state (field 3), so utime (field 14) is fields[11] and
	// stime (field 15) is fields[12].
	if len(fields) < 13 {
		return 0, 0, false
	}
	utime, err := strconv.ParseUint(fields[11], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	stime, err := strconv.ParseUint(fields[12], 10, 64)
	if err != nil {
		return 0, 0, false
	}

	var rssBytes int64
	statusBytes, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err == nil {
		for _, line := range strings.Split(string(statusBytes), "\n") {
			if !strings.HasPrefix(line, "VmRSS:") {
				continue
			}
			valueFields := strings.Fields(strings.TrimPrefix(line, "VmRSS:"))
			if len(valueFields) == 0 {
				break
			}
			rssKiB, err := strconv.ParseInt(valueFields[0], 10, 64)
			if err == nil {
				rssBytes = rssKiB * 1024
			}
			break
		}
	}
	return utime + stime, rssBytes, true
}
