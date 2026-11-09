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
}

// recordStopped marks the process as exited.
func (t *Transcoder) recordStopped() {
	t.state.mu.Lock()
	defer t.state.mu.Unlock()
	t.state.running = false
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

// ArgvSections are the structured argv groups shown by the admin FFmpeg panel.
type ArgvSections struct {
	Input       []string
	Video       []string
	Audio       []string
	Output      []string
	Other       []string
	Environment []string
}

// ClassifyArgv groups an executed argv into the panel's collapsible sections.
// Classification is by flag family, so it is display-only and intentionally
// lossless (every argument appears exactly once across the sections).
// Argv classification is a flat flag-family matcher; splitting it would obscure
// the display grouping rules.
//
//nolint:cyclop // flat flag-family classification
func ClassifyArgv(argv []string, environment []string) ArgvSections {
	sections := ArgvSections{
		Input:       make([]string, 0, 4),
		Video:       make([]string, 0, 16),
		Audio:       make([]string, 0, 8),
		Output:      make([]string, 0, 16),
		Other:       make([]string, 0, 8),
		Environment: environment,
	}

	for index := 0; index < len(argv); index++ {
		arg := argv[index]
		value := ""
		hasValue := index+1 < len(argv)
		if hasValue {
			value = argv[index+1]
		}

		switch {
		case arg == "-i":
			sections.Input = append(sections.Input, arg)
			if hasValue {
				sections.Input = append(sections.Input, value)
				index++
			}
		case strings.HasPrefix(arg, "-c:v") || strings.HasPrefix(arg, "-b:v") ||
			strings.HasPrefix(arg, "-maxrate") || strings.HasPrefix(arg, "-bufsize") ||
			strings.HasPrefix(arg, "-g:") || strings.HasPrefix(arg, "-keyint_min") ||
			strings.HasPrefix(arg, "-r:") || arg == "-g" || arg == "-keyint_min" ||
			arg == ffmpegFlagPreset || arg == ffmpegFlagTune || arg == "-profile:v" || arg == "-bf" ||
			arg == "-pix_fmt" || arg == "-x264-params" || arg == "-sc_threshold" ||
			arg == "-flags" || arg == ffmpegFlagMap && hasValue && strings.HasPrefix(value, "0:v"):
			sections.Video = append(sections.Video, arg)
			if hasValue && !strings.HasPrefix(arg, "-map") {
				sections.Video = append(sections.Video, value)
				index++
			}
		case strings.HasPrefix(arg, "-c:a") || strings.HasPrefix(arg, "-b:a") ||
			arg == "-ar" || arg == "-ac" || arg == "-application" ||
			arg == "-frame_duration" || arg == "-fec" || arg == "-dtx" ||
			arg == ffmpegFlagMap && hasValue && strings.HasPrefix(value, "0:a"):
			sections.Audio = append(sections.Audio, arg)
			if hasValue && !strings.HasPrefix(arg, "-map") {
				sections.Audio = append(sections.Audio, value)
				index++
			}
		case strings.HasPrefix(arg, "-hls_") || arg == "-f" || arg == "-method" ||
			arg == "-master_pl_name" || arg == "-segment_format_options" ||
			arg == "-max_muxing_queue_size" || arg == "-payload_type" ||
			arg == "-bsf:v" || arg == "-var_stream_map" || strings.HasPrefix(arg, "http://") ||
			strings.HasPrefix(arg, "rtp://") || strings.HasPrefix(arg, "srt://") ||
			arg == "-flush_packets":
			sections.Output = append(sections.Output, arg)
			if hasValue && !strings.HasPrefix(arg, "-map") &&
				!strings.HasPrefix(arg, "http://") && !strings.HasPrefix(arg, "rtp://") &&
				!strings.HasPrefix(arg, "srt://") {
				// Flags that take an explicit value and are not a target URL.
				if strings.HasPrefix(arg, "-") && arg != "-map" {
					sections.Output = append(sections.Output, value)
					index++
				}
			}
		default:
			sections.Other = append(sections.Other, arg)
		}
	}
	return sections
}
