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
	ffmpegFlagPreset    = "-preset"
	// ffmpegTuneZeroLatency is the libx264 low-latency tuning name.
	ffmpegTuneZeroLatency = "zerolatency"
)

// rtpOutputFlags renders argv for one complete WebRTC RTP output (video and
// audio). Multi-leg processes use rtpVideoOutputFlags plus one shared audio
// output so Opus is encoded once for all legs.
func (t *Transcoder) rtpOutputFlags(output RTPOutput) []string {
	args := t.rtpVideoOutputFlags(output)
	return append(args, t.rtpAudioOutputFlags(output)...)
}

// rtpVideoOutputFlags renders the video RTP output for one leg.
func (t *Transcoder) rtpVideoOutputFlags(output RTPOutput) []string {
	args := make([]string, 0, 24)

	// Video leg.
	args = append(args, ffmpegFlagMap, "0:v:0")
	if output.CopyVideo {
		args = append(args, "-c:v", "copy")
	} else {
		legCodec := t.codec
		if output.VideoCodec != "" && output.VideoCodec != t.codec.Name() {
			legCodec = getCodec(output.VideoCodec)
		}
		args = append(args, "-c:v", legCodec.Name())
		// Preset/tune names are encoder specific: h264_nvenc rejects the
		// libx264-only "zerolatency" tune (and VA-API/V4L2/VideoToolbox have no
		// tune at all). A wrong tune makes FFmpeg exit at startup, which used to
		// take the whole shared process (HLS + publisher) down with it.
		args = append(args, rtpEncoderTuning(legCodec, output.CPUUsageLevel)...)
		if filters := legCodec.ExtraFilters(); filters != "" {
			args = append(args, "-filter:v", filters)
		}
		args = append(
			args,
			"-profile:v", normalizeH264Profile(output.H264Profile),
			"-bf", "0",
			"-g", strconv.Itoa(output.GOPFrames),
			"-keyint_min", strconv.Itoa(output.GOPFrames),
			"-sc_threshold", "0",
			"-b:v", strconv.Itoa(output.VideoBitrateKbps)+"k",
			"-maxrate", strconv.Itoa(output.VideoBitrateKbps)+"k",
			"-bufsize", strconv.Itoa(output.VideoBitrateKbps)+"k",
			"-pix_fmt", legCodec.PixelFormat(),
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

	return args
}

// rtpAudioOutputFlags renders one standalone Opus RTP output for a leg. It is
// used for single-leg processes and as the fallback when legs disagree on Opus
// settings (their audio cannot be shared then).
func (t *Transcoder) rtpAudioOutputFlags(output RTPOutput) []string {
	opusBitrate, opusFrame := output.opusSettings()
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
	return audioArgs
}

// opusSettings applies the WebRTC audio defaults.
func (o RTPOutput) opusSettings() (bitrateKbps, frameMs int) {
	bitrateKbps = o.OpusBitrateKbps
	if bitrateKbps <= 0 {
		bitrateKbps = 96
	}
	frameMs = o.OpusFrameMs
	if frameMs <= 0 {
		frameMs = 20
	}
	return bitrateKbps, frameMs
}

// sharedAudioCompatible reports whether all legs use identical Opus settings
// and payload type, so one encoded audio stream can feed every leg.
func sharedAudioCompatible(outputs []RTPOutput) bool {
	if len(outputs) < 2 {
		return false
	}
	bitrate, frame := outputs[0].opusSettings()
	payloadType := outputs[0].AudioPayloadType
	for _, output := range outputs[1:] {
		otherBitrate, otherFrame := output.opusSettings()
		if otherBitrate != bitrate || otherFrame != frame || output.AudioPayloadType != payloadType {
			return false
		}
	}
	return true
}

// sharedRTPAudioFlags renders a single Opus encode fanned out to every WebRTC
// leg with the tee muxer. Each RTP slave receives only the one audio stream, so
// no stream selection is required.
func (t *Transcoder) sharedRTPAudioFlags(outputs []RTPOutput) []string {
	if !sharedAudioCompatible(outputs) {
		return nil
	}
	opusBitrate, opusFrame := outputs[0].opusSettings()
	args := []string{
		ffmpegFlagMap, "0:a:0?",
		"-c:a", "libopus",
		"-b:a", strconv.Itoa(opusBitrate) + "k",
		"-ar", "48000",
		"-ac", "2",
		"-application", "lowdelay",
		"-frame_duration", strconv.Itoa(opusFrame),
		"-f", "tee",
	}
	slaves := make([]string, 0, len(outputs))
	for _, output := range outputs {
		slaves = append(slaves,
			"[f=rtp:payload_type="+strconv.Itoa(output.AudioPayloadType)+"]"+withRTPPacketSize(output.AudioURL))
	}
	args = append(args, strings.Join(slaves, "|"))

	return args
}

// nvencTuneLowLatency is the low-latency tuning name accepted by h264_nvenc
// ("zerolatency" is a libx264 tuning and is rejected by NVENC).
const nvencTuneLowLatency = "ll"

// rtpEncoderTuning returns the encoder-specific preset/tune arguments for a
// WebRTC transcode leg. Only flags the encoder actually supports are emitted.
func rtpEncoderTuning(codec Codec, cpuUsageLevel int) []string {
	preset := codec.GetPresetForLevel(cpuUsageLevel)
	switch codec.(type) {
	case *Libx264Codec:
		return []string{ffmpegFlagPreset, preset, ffmpegFlagTune, ffmpegTuneZeroLatency}
	case *NvencCodec:
		return []string{ffmpegFlagPreset, preset, ffmpegFlagTune, nvencTuneLowLatency}
	case *QuicksyncCodec:
		return []string{ffmpegFlagPreset, preset}
	default:
		// VA-API, V4L2, VideoToolbox and OMX expose no preset/tune option that
		// maps to the HLS variant settings; omit them rather than fail startup.
		return nil
	}
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
