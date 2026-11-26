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
	// BitratePeakKbps is the ceiling of the leg's rate-control window. Zero
	// means "same as VideoBitrateKbps" (a pinned/CBR leg).
	BitratePeakKbps int
	// Scale is the "W:H" expression a transcode leg must scale to. Empty when
	// the leg keeps the source resolution (copy legs, or a variant without
	// scaled dimensions).
	Scale     string
	GOPFrames int
	// Framerate is the leg's output frame rate. Zero leaves the source rate.
	Framerate   int
	CopyVideo   bool
	H264Profile string

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

// bitrateWindowKbps is how far a VBR encode may drift from its target. It is
// capped at half the target so a low-bitrate rendition cannot declare a peak
// several times its average: at 400 kbps a flat 2000 kbps window declared a 2.2
// Mbps peak for a 400 kbps stream, and the master playlist advertised it.
const bitrateWindowKbps = 2000

// bitrateDeltaKbps is the half-width of the window for one target.
func bitrateDeltaKbps(targetKbps int) int {
	if targetKbps <= 0 {
		return 0
	}

	delta := targetKbps / 2
	if delta > bitrateWindowKbps {
		delta = bitrateWindowKbps
	}

	return delta
}

// ScaleExpression renders the ffmpeg scale expression for a variant, keeping the
// aspect ratio when only one dimension is configured. Empty means no scaling.
func ScaleExpression(width, height int) string {
	switch {
	case width > 0 && height > 0:
		return strconv.Itoa(width) + ":" + strconv.Itoa(height)
	case width > 0:
		return strconv.Itoa(width) + ":-2"
	case height > 0:
		return "-2:" + strconv.Itoa(height)
	}

	return ""
}

// scThresholdFlags returns the scene change threshold flag only for the encoder that defines it.
// sc_threshold is a libx264 AVOption; hardware encoders such as h264_nvenc do not use it and
// report "Codec AVOption sc_threshold has not been used for any stream" on every start, once per
// output, which buries the real startup diagnostics in the log.
func scThresholdFlags(codec Codec) []string {
	if codec == nil || codec.Name() != codecNameLibx264 {
		return nil
	}

	return []string{"-sc_threshold", "0"}
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
		// One filtergraph, scale first: FFmpeg keeps only the last -filter:v for a
		// stream, so emitting two silently dropped the hardware upload filters and
		// made VAAPI/QSV legs fail to start (taking the shared HLS process with them).
		// The HLS path joins them the same way, in the same order.
		filters := make([]string, 0, 3)
		// If the process decodes into CUDA VRAM frames (-hwaccel_output_format cuda)
		// while this RTP leg uses a CPU encoder (e.g. libx264), normalize to 8-bit
		// nv12 on GPU (handling 10-bit HEVC p010le) before downloading to host memory.
		if t.codec != nil && t.codec.PixelFormat() == "cuda" && legCodec.PixelFormat() != "cuda" {
			filters = append(filters, "scale_cuda=format=nv12", "hwdownload", "format=nv12")
		}
		if output.Scale != "" {
			scaler := legCodec.Scaler()
			if scaler == "" {
				scaler = "scale"
			}
			scaleExpr := scaler + "=" + output.Scale
			if scaler == "scale_cuda" {
				scaleExpr += ":format=nv12"
			}

			filters = append(filters, scaleExpr)
		} else if legCodec.Scaler() == "scale_cuda" {
			filters = append(filters, "scale_cuda=format=nv12")
		}
		if extra := legCodec.ExtraFilters(); extra != "" {
			filters = append(filters, extra)
		}
		if len(filters) > 0 {
			args = append(args, "-filter:v", strings.Join(filters, ","))
		}

		// Unlike HLS, the WebRTC leg gets no rate feedback from the browser (GCC runs
		// on the sender side, not back into FFmpeg), so a wide VBR window turns into
		// burst loss and jitter-buffer growth instead of recovered quality. The
		// window is therefore narrow (target .. target + half the target, or the
		// operator's explicit peak) and fixed when the stream starts: a source
		// bitrate that moves later never restarts the encoder.
		targetKbps := output.VideoBitrateKbps
		minKbps := targetKbps
		maxKbps := output.rateCeilingKbps()

		args = append(
			args,
			"-profile:v", normalizeH264Profile(output.H264Profile),
			"-bf", "0",
			"-g", strconv.Itoa(output.GOPFrames),
			"-keyint_min", strconv.Itoa(output.GOPFrames),
		)
		if output.Framerate > 0 {
			args = append(args, "-r", strconv.Itoa(output.Framerate))
		}
		args = append(args, scThresholdFlags(legCodec)...)
		args = append(
			args,
			"-b:v", strconv.Itoa(targetKbps)+"k",
			"-minrate", strconv.Itoa(minKbps)+"k",
			"-maxrate", strconv.Itoa(maxKbps)+"k",
			"-bufsize", strconv.Itoa(maxKbps)+"k",
			"-pix_fmt", legCodec.PixelFormat(),
		)
	}
	// In-band SPS/PPS on every IDR is mandatory for browser copy legs and for
	// mid-stream joins on transcode legs. Copy sources may be AVCC/FLV without
	// parameter sets, so convert them before dump_extra injects the extradata.
	bitstreamFilter := "dump_extra=freq=keyframe"
	if output.CopyVideo {
		bitstreamFilter = "h264_mp4toannexb,dump_extra=freq=keyframe"
	}
	args = append(
		args,
		"-bsf:v", bitstreamFilter,
		"-f", "rtp",
		"-payload_type", strconv.Itoa(output.VideoPayloadType),
		withRTPPacketSize(output.VideoURL),
	)

	return args
}

// opusEncoderFlags is the single definition of WebRTC's Opus encoder options.
// All WebRTC audio paths (single leg, per-leg fallback, shared tee) use it so
// they cannot drift apart.
func opusEncoderFlags(bitrateKbps, frameMs int) []string {
	return []string{
		ffmpegFlagMap, "0:a:0?",
		"-c:a", "libopus",
		"-b:a", strconv.Itoa(bitrateKbps) + "k",
		"-ar", "48000",
		"-ac", "2",
		"-application", "lowdelay",
		"-frame_duration", strconv.Itoa(frameMs),
	}
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
	audioArgs := opusEncoderFlags(opusBitrate, opusFrame)
	audioArgs = append(
		audioArgs,
		"-f", "rtp",
		"-payload_type", strconv.Itoa(output.AudioPayloadType),
		withRTPPacketSize(output.AudioURL),
	)
	return audioArgs
}

// opusSettings applies the WebRTC audio defaults. The frame duration and bitrate are the operator's
// settings and are used as configured: the encoder keeps its normal variable bitrate behaviour.
// rateCeilingKbps is the leg's rate-control ceiling: the explicit peak when it
// is above the target, otherwise the target itself (a pinned leg).
func (o RTPOutput) rateCeilingKbps() int {
	if o.BitratePeakKbps > o.VideoBitrateKbps {
		return o.BitratePeakKbps
	}

	return o.VideoBitrateKbps
}

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

// allLegsShareOpusSettings reports whether at least one leg is present and all
// legs use identical Opus settings and payload type, so one encoded Opus stream
// can feed every leg.
func allLegsShareOpusSettings(outputs []RTPOutput) bool {
	if len(outputs) == 0 {
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

// sharedAudioCompatible reports whether multiple legs use identical Opus
// settings and payload type, so a standalone audio tee can replace per-leg
// Opus encodes.
func sharedAudioCompatible(outputs []RTPOutput) bool {
	return len(outputs) >= 2 && allLegsShareOpusSettings(outputs)
}

// sharedRTPAudioFlags renders a single Opus encode fanned out to every WebRTC
// leg with the tee muxer. Each RTP slave receives only the one audio stream, so
// no stream selection is required.
func (t *Transcoder) sharedRTPAudioFlags(outputs []RTPOutput) []string {
	if !sharedAudioCompatible(outputs) {
		return nil
	}
	opusBitrate, opusFrame := outputs[0].opusSettings()
	args := opusEncoderFlags(opusBitrate, opusFrame)
	args = append(args, "-f", "tee")
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
