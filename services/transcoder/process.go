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

// SetRestartCount records how many times the supervisor has restarted this
// transcoder role within the current live stream.
func (t *Transcoder) SetRestartCount(count int) {
	t.state.mu.Lock()
	defer t.state.mu.Unlock()
	t.state.restarts = count
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

// outputBlock is one ffmpeg output: the "-f <muxer>" flags, its options and
// the target URL(s), in their original order. An output block starts at
// "-f <muxer>" and runs to the next "-f" (or the end of the argv). Blocks are
// discovered after the input, so an input-side "-f mpegts" never counts.
type outputBlock struct {
	start int // index in the post-input argv where "-f" sits
	end   int // index just past the block
	muxer string
	argv  []string // the block's arguments in their original order
}

// isTargetArgument reports whether an argument is an output target. Owncast's
// targets are always URLs (the HLS listener, an RTP loopback) or a tee slave
// list, which starts with the slave's option bracket ([use_fifo=1:...] or
// [select=...]); ffmpeg option values never start with "[" or a URL scheme.
// This is what makes the block boundary exact even though ffmpeg puts the next
// output's options after this block's target.
func isTargetArgument(arg string) bool {
	return strings.HasPrefix(arg, "http://") ||
		strings.HasPrefix(arg, "https://") ||
		strings.HasPrefix(arg, "rtp://") ||
		strings.HasPrefix(arg, "udp://") ||
		strings.HasPrefix(arg, "[")
}

// splitOutputBlocks walks the post-input argv and returns every output block in
// order. ffmpeg's layout is "[options] target" per output, so a block is
// everything from the previous block's target up to (and including) this
// block's target; the muxer is the "-f" inside it. That also puts the options
// that precede "-f" — the encode settings of the output that follows — into the
// block they belong to.
func splitOutputBlocks(argv []string) []outputBlock {
	var blocks []outputBlock
	blockStart := 0
	muxer := ""
	for index := 0; index < len(argv); index++ {
		if !isTargetArgument(argv[index]) {
			continue
		}
		if m := findMuxer(argv[blockStart : index+1]); m != "" {
			muxer = m
		}
		blocks = append(blocks, outputBlock{
			start: blockStart,
			end:   index + 1,
			muxer: muxer,
			argv:  argv[blockStart : index+1],
		})
		blockStart = index + 1
		muxer = ""
	}
	if blockStart < len(argv) {
		// A tail without a target: keep it lossless with the last block's muxer.
		blocks = append(blocks, outputBlock{start: blockStart, end: len(argv), muxer: muxer, argv: argv[blockStart:]})
	}

	return blocks
}

// findMuxer returns the "-f" value inside a block, or "" when there is none.
func findMuxer(block []string) string {
	for index, arg := range block {
		if arg == "-f" && index+1 < len(block) {
			return block[index+1]
		}
	}

	return ""
}

// muxer names used by the classifier. They are ffmpeg muxers, not configuration
// values, but the classifier compares them in three places.
const (
	muxerHLS = "hls"
	muxerRTP = "rtp"
	muxerTee = "tee"
)

// isWebRTCMuxer reports whether an output muxer feeds a WebRTC RTP leg.
func isWebRTCMuxer(muxer string) bool {
	return muxer == muxerRTP || muxer == muxerTee
}

// teeCarriesHLS reports whether a tee block has at least one f=hls slave.
func teeCarriesHLS(block outputBlock) bool {
	if len(block.argv) == 0 {
		return false
	}

	return strings.Contains(block.argv[len(block.argv)-1], "f="+muxerHLS)
}

// ClassifyArgv groups an executed argv into the panel's collapsible sections.
//
// Classification walks the output blocks: each "-f <muxer>" after the input
// starts one, and everything from it to the next "-f" (or the end) belongs to
// it, in its original order. An HLS block (-f hls, options and playlist URL)
// goes to the HLS section; the RTP and tee blocks go to the WebRTC section,
// because that is the stream a WebRTC viewer receives — including the Opus
// encode options and the pipe-joined tee targets.
//
// The shared HLS+WebRTC layout uses one tee muxer for every target. There the
// tee's single pipe-joined argument is expanded into one entry per slave, split
// by muxer (f=hls → HLS, f=rtp → WebRTC). The encode options that feed a mixed
// tee serve both protocols, so they stay in Common; a pure-RTP tee keeps its
// encode options in WebRTC, where its viewers' playback actually comes from.
//
// argv itself is never modified; only this display view is, and the full argv
// stays available to the panel for copy-and-inspect.
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

	// Everything up to and including the input ("-i pipe:0") is common.
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

	blocks := splitOutputBlocks(rest)
	if len(blocks) == 0 {
		// No output block found: keep the tail lossless under Common.
		sections.Common = append(sections.Common, rest...)
		return sections
	}

	for _, block := range blocks {
		if block.muxer == "tee" && teeCarriesHLS(block) {
			// A mixed tee: the encode options before it serve both protocols, so
			// they describe the stream best from Common; the slave list is
			// expanded, each slave shown under its protocol. The block already
			// contains "-f tee", so no marker is added here.
			last := block.argv[len(block.argv)-1]
			sections.Common = append(sections.Common, block.argv[:len(block.argv)-1]...)
			for _, slave := range strings.Split(last, "|") {
				if strings.Contains(slave, "f="+muxerHLS) {
					sections.HLS = append(sections.HLS, slave)

					continue
				}
				sections.WebRTC = append(sections.WebRTC, slave)
			}

			continue
		}

		if isWebRTCMuxer(block.muxer) {
			sections.WebRTC = append(sections.WebRTC, block.argv...)
			if block.muxer == "tee" {
				// Expand the slave list for readability.
				last := block.argv[len(block.argv)-1]
				sections.WebRTC = sections.WebRTC[:len(sections.WebRTC)-1]
				sections.WebRTC = append(sections.WebRTC, strings.Split(last, "|")...)
			}

			continue
		}

		sections.HLS = append(sections.HLS, block.argv...)
	}

	return sections
}
