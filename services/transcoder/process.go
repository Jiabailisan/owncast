package transcoder

import (
	"strings"
	"sync"
	"time"
)

// ProcessSnapshot is the inspectable state of one transcoder process. The argv
// is the exact argv passed to exec (never a recomputation), so the admin FFmpeg
// panel is exact by construction (spec 6.4/T14). Callers must redact it before
// display.
type ProcessSnapshot struct {
	ID            string
	Purpose       []string
	PID           int
	StartedAt     time.Time
	UptimeSeconds float64
	RestartCount  int
	Running       bool
	Argv          []string

	// Live metrics sampled while the process runs (nil = N/A).
	Speed          *float64
	CPUPercent     *float64
	RSSBytes       *int64
	GPUPercent     *float64
	GPUMemoryBytes *int64
	GPUName        string
}

// stateMu guards the live-process state that the FFmpeg panel reads while the
// transcoder goroutine writes it.
type processState struct {
	mu        sync.RWMutex
	purpose   []string
	startedAt time.Time
	pid       int
	argv      []string
	running   bool
	restarts  int

	// metrics are written by the progress scanner and the CPU/GPU sampler.
	metrics processMetrics
}

// SetPurpose labels this process for the FFmpeg panel (e.g. hls, webrtc).
func (t *Transcoder) SetPurpose(purpose []string) {
	t.state.mu.Lock()
	defer t.state.mu.Unlock()
	t.state.purpose = append([]string(nil), purpose...)
}

// Snapshot returns the current process state for the admin FFmpeg panel.
func (t *Transcoder) Snapshot() ProcessSnapshot {
	t.state.mu.RLock()
	defer t.state.mu.RUnlock()

	snapshot := ProcessSnapshot{
		ID:           t.segmentIdentifier,
		Purpose:      append([]string(nil), t.state.purpose...),
		PID:          t.state.pid,
		StartedAt:    t.state.startedAt,
		RestartCount: t.state.restarts,
		Running:      t.state.running,
		Argv:         append([]string(nil), t.state.argv...),
	}
	if !snapshot.StartedAt.IsZero() {
		snapshot.UptimeSeconds = time.Since(snapshot.StartedAt).Seconds()
	}

	metrics := t.state.metrics.snapshot()
	snapshot.Speed = metrics.Speed
	snapshot.CPUPercent = metrics.CPUPercent
	snapshot.RSSBytes = metrics.RSSBytes
	snapshot.GPUPercent = metrics.GPUPercent
	snapshot.GPUMemoryBytes = metrics.GPUMemoryBytes
	snapshot.GPUName = metrics.GPUName
	if snapshot.ID == "" {
		snapshot.ID = "transcoder"
	}
	if len(snapshot.Purpose) == 0 {
		snapshot.Purpose = []string{"hls"}
	}
	return snapshot
}

// recordStarted stores the live process information once exec succeeds.
func (t *Transcoder) recordStarted(argv []string, pid int) {
	t.state.mu.Lock()
	defer t.state.mu.Unlock()
	t.state.argv = append([]string(nil), argv...)
	t.state.pid = pid
	t.state.startedAt = time.Now()
	t.state.running = true
	t.state.metrics.reset()
}

// recordStopped marks the process as exited.
func (t *Transcoder) recordStopped() {
	t.state.mu.Lock()
	defer t.state.mu.Unlock()
	t.state.running = false
	t.state.metrics.reset()
}

// RedactArgv returns a copy of argv with known secret values replaced by
// "***". It matches by secret value (the caller passes the configured
// secrets) and by sensitive flag names, so operator secrets never reach the
// admin FFmpeg panel or its copy button (spec 6.4/9).
func RedactArgv(argv []string, secrets []string) []string {
	redacted := make([]string, len(argv))
	copy(redacted, argv)

	for index := range redacted {
		if isSecretArgValue(argv, index) || containsSecret(redacted[index], secrets) {
			redacted[index] = "***"
		}
	}
	return redacted
}

// isSecretArgValue reports whether argv[index] is the value of a
// secret-named flag. It handles both "--flag value" and "--flag=value" forms.
func isSecretArgValue(argv []string, index int) bool {
	if index == 0 {
		return false
	}
	previous := strings.ToLower(argv[index-1])
	if strings.HasPrefix(previous, "-") && strings.ContainsAny(previous, "=:") {
		return false
	}
	if strings.Contains(previous, "passphrase") || strings.Contains(previous, "password") ||
		strings.Contains(previous, "secret") || strings.Contains(previous, "token") ||
		strings.Contains(previous, "credential") {
		return true
	}
	value := strings.ToLower(argv[index])
	for _, prefix := range []string{"passphrase=", "password=", "secret=", "token=", "credential="} {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func containsSecret(arg string, secrets []string) bool {
	if len(secrets) == 0 {
		return false
	}
	for _, secret := range secrets {
		if secret != "" && strings.Contains(arg, secret) {
			return true
		}
	}
	return false
}

// ArgvSections are the structured argv groups shown by the admin FFmpeg panel:
// common (global/input flags shared by every output), hls (the HLS output
// block), webrtc (the RTP output blocks) and the separate redacted environment.
type ArgvSections struct {
	Common      []string
	HLS         []string
	WebRTC      []string
	Environment []string
}

// findHLSPlaylistEnd returns the index just past the HLS playlist target URL in
// the output block, or -1 when the argv has no HLS output. The playlist URL is
// the last HLS argument and ends in "/stream.m3u8"; the segment-filename URL
// appears earlier in the same block and must not split it.
func findHLSPlaylistEnd(argv []string) int {
	fallback := -1
	for index, arg := range argv {
		if !strings.HasPrefix(arg, "http://") && !strings.HasPrefix(arg, "https://") {
			continue
		}
		if strings.HasSuffix(arg, "stream.m3u8") {
			return index + 1
		}
		fallback = index + 1
	}
	return fallback
}

// ClassifyArgv groups an executed argv into the panel's collapsible sections.
// Classification is structural, not flag-based: everything up to and including
// the input ("-i pipe:0") is common; for HLS processes the HLS output block
// runs through the in-process HLS playlist URL; everything after it is a WebRTC
// RTP output block (and for the isolated WebRTC-only process everything after
// the input is WebRTC). Every element of argv appears exactly once across
// Common+HLS+WebRTC by construction; Environment is not part of argv.
func ClassifyArgv(argv []string, environment []string) ArgvSections {
	sections := ArgvSections{
		Common:      make([]string, 0, 16),
		HLS:         make([]string, 0, 16),
		WebRTC:      make([]string, 0, 16),
		Environment: environment,
	}
	if len(argv) == 0 {
		return sections
	}

	inputEnd := -1
	for index, arg := range argv {
		if arg == "-i" {
			inputEnd = index + 1
			if inputEnd < len(argv) {
				inputEnd++
			}
			break
		}
	}
	if inputEnd < 0 {
		// Unknown shape: keep it classified but lossless.
		sections.Common = append(sections.Common, argv...)
		return sections
	}
	sections.Common = append(sections.Common, argv[:inputEnd]...)

	rest := argv[inputEnd:]
	hlsEnd := findHLSPlaylistEnd(rest)
	if hlsEnd >= 0 {
		sections.HLS = append(sections.HLS, rest[:hlsEnd]...)
		sections.WebRTC = append(sections.WebRTC, rest[hlsEnd:]...)
		return sections
	}

	// No HLS target: the isolated WebRTC-only process. Everything after the
	// input is an RTP output block.
	sections.WebRTC = append(sections.WebRTC, rest...)
	return sections
}
