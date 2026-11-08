package transcoder

import (
	"strconv"
	"strings"
)

// RTPOutput describes one WebRTC media leg produced by ffmpeg into a pair of
// Go-owned loopback UDP sockets. The plan builder fills this from the WebRTC
// configuration and output variants; the transcoder only renders argv from it.
type RTPOutput struct {
	// VariantIndex is the configured output variant this leg belongs to.
	VariantIndex int

	// VideoURL/AudioURL are ffmpeg RTP targets such as
	// "rtp://127.0.0.1:41234?pkt_size=1200". The UDP sockets are already
	// bound by Go before the process starts (no reserve-then-reuse race).
	VideoURL string
	AudioURL string

	// Payload types used by the browser media engine (canonical fmtp).
	VideoPayloadType int
	AudioPayloadType int

	// Video encoding settings.
	VideoBitrateKbps int
	GOPFrames        int
	CopyVideo        bool
	H264Profile      string

	// Audio encoding settings (WebRTC audio is always Opus). OpusFEC/OpusDTX
	// are recorded for the settings metadata but currently have no FFmpeg
	// argv representation (see rtpOutputFlags); they are validated and stored
	// so a future FFmpeg build can apply them.
	OpusBitrateKbps int
	OpusFrameMs     int
	OpusFEC         bool
	OpusDTX         bool

	// VideoCodec is the configured encoder name for transcode legs (e.g.
	// "libx264"). Ignored when CopyVideo is set.
	VideoCodec string

	// CPUUsageLevel maps to the encoder preset exactly like HLS variants.
	CPUUsageLevel int
}

// rtpMuxerPacketSize is appended to RTP URLs when the caller did not provide
// one (the WebRTC leg API always does).
const rtpMuxerPacketSize = 1200

// Shared argv/encoder value constants (also used by codecs.go and process.go).
const (
	h264ProfileBaseline = "baseline"
	h264ProfileMain     = "main"
	h264ProfileHigh     = "high"
	ffmpegFlagTune      = "-tune"
)

// rtpOutputFlags renders argv for one WebRTC RTP output. It is a separate
// output context that follows the HLS output, so its options only apply to the
// RTP muxers.
func (t *Transcoder) rtpOutputFlags(output RTPOutput) []string {
	args := make([]string, 0, 40)

	// Video leg.
	args = append(args, ffmpegFlagMap, "0:v:0")
	if output.CopyVideo {
		args = append(args, "-c:v", "copy")
	} else {
		codecName := output.VideoCodec
		if codecName == "" {
			codecName = t.codec.Name()
		}
		args = append(
			args,
			"-c:v", codecName,
			"-preset", t.codec.GetPresetForLevel(output.CPUUsageLevel),
			"-tune", "zerolatency",
			"-profile:v", normalizeH264Profile(output.H264Profile),
			"-bf", "0",
			"-g", strconv.Itoa(output.GOPFrames),
			"-keyint_min", strconv.Itoa(output.GOPFrames),
			"-sc_threshold", "0",
			"-b:v", strconv.Itoa(output.VideoBitrateKbps)+"k",
			"-maxrate", strconv.Itoa(output.VideoBitrateKbps)+"k",
			"-bufsize", strconv.Itoa(output.VideoBitrateKbps)+"k",
			"-pix_fmt", t.codec.PixelFormat(),
		)
	}
	// In-band SPS/PPS on every IDR is mandatory for browser copy legs and for
	// mid-stream joins on transcode legs.
	args = append(
		args,
		"-bsf:v", "dump_extra=freq=keyframe",
		"-f", "rtp",
		"-payload_type", strconv.Itoa(output.VideoPayloadType),
		withRTPPacketSize(output.VideoURL),
	)

	// Audio leg: WebRTC audio is always Opus.
	opusBitrate := output.OpusBitrateKbps
	if opusBitrate <= 0 {
		opusBitrate = 96
	}
	opusFrame := output.OpusFrameMs
	if opusFrame <= 0 {
		opusFrame = 20
	}
	// Opus encoding for WebRTC. libopus in-band FEC/DTX flags are NOT emitted:
	// FFmpeg's RTP muxer also defines "-fec" (expecting an FEC protocol name),
	// so the encoder option cannot be selected unambiguously from the CLI and
	// FFmpeg rejects it with "Unsupported FEC protocol". Loss recovery relies on
	// the browser's NACK/PLC (see the "Not supported" documentation table).
	audioArgs := make([]string, 0, 19)
	audioArgs = append(audioArgs, []string{
		ffmpegFlagMap, "0:a:0?",
		"-c:a", "libopus",
		"-b:a", strconv.Itoa(opusBitrate) + "k",
		"-ar", "48000",
		"-ac", "2",
		"-application", "lowdelay",
		"-frame_duration", strconv.Itoa(opusFrame),
	}...)
	audioArgs = append(
		audioArgs,
		"-f", "rtp",
		"-payload_type", strconv.Itoa(output.AudioPayloadType),
		withRTPPacketSize(output.AudioURL),
	)
	args = append(args, audioArgs...)
	return args
}

// normalizeH264Profile maps the configuration profile names to the profile
// names accepted by the encoders. "constrained-baseline"-style names are
// browser concepts; libx264 accepts baseline/main/high.
func normalizeH264Profile(profile string) string {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case h264ProfileBaseline, "constrained-baseline":
		return h264ProfileBaseline
	case h264ProfileMain:
		return h264ProfileMain
	case h264ProfileHigh, "constrained-high":
		return h264ProfileHigh
	default:
		return h264ProfileMain
	}
}

// withRTPPacketSize ensures the RTP URL carries a packet size, matching the
// WebRTC configuration's rtpPacketSize expectation when the caller omitted it.
func withRTPPacketSize(url string) string {
	if url == "" || strings.Contains(url, "pkt_size=") {
		return url
	}
	separator := "?"
	if strings.Contains(url, "?") {
		separator = "&"
	}
	return url + separator + "pkt_size=" + strconv.Itoa(rtpMuxerPacketSize)
}
