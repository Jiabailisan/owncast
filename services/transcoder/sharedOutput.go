package transcoder

import (
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/owncast/owncast/config"

	log "github.com/sirupsen/logrus"
	"github.com/teris-io/shortid"
)

// sharedOutputEnabled reports whether HLS and the WebRTC RTP legs can be fed
// from one tee output. That output encodes each HLS transcode variant exactly
// once (WebRTC reuses the same encoded stream) and encodes WebRTC's Opus audio
// exactly once for every leg. It requires at least one RTP leg, variants whose
// WebRTC legs map back to them, and identical Opus settings across legs.
// Isolated-process and S3 configurations keep the legacy layout.
func (t *Transcoder) sharedOutputEnabled() bool {
	if t.rtpOnly || len(t.rtpOutputs) == 0 || len(t.variants) == 0 {
		return false
	}
	if !t.codec.SupportsSharedTranscode() {
		return false
	}
	if t.configRepository != nil && t.configRepository.GetS3Config().Enabled {
		return false
	}
	if !sharedAudioCompatible(t.rtpOutputs) {
		return false
	}
	for _, output := range t.rtpOutputs {
		if _, ok := t.sharedVideoVariantFor(output); !ok {
			return false
		}
	}
	for _, output := range t.rtpOutputs {
		if output.VariantIndex < 0 || output.VariantIndex >= len(t.variants) {
			return false
		}
	}

	return true
}

// sharedVideoVariantFor maps a WebRTC leg to the HLS variant whose encoded
// stream it can reuse. A transcode leg must select a transcoded HLS variant and
// a copy leg a passthrough variant; the same logical index is preferred, then
// any variant with matching encoding. The false return means the leg cannot be
// shared and the caller must fall back to the legacy layout.
func (t *Transcoder) sharedVideoVariantFor(output RTPOutput) (int, bool) {
	if output.VariantIndex >= 0 && output.VariantIndex < len(t.variants) {
		if t.variantMatchesLeg(t.variants[output.VariantIndex], output) {
			return output.VariantIndex, true
		}
	}
	for _, variant := range t.variants {
		if t.variantMatchesLeg(variant, output) {
			return variant.index, true
		}
	}

	return 0, false
}

// variantMatchesLeg reports whether a variant's encode can feed this leg. The
// passthrough flag must agree: a transcode leg must never be fed the raw source
// stream, and a copy leg must never be fed an encoded one.
func (t *Transcoder) variantMatchesLeg(variant HLSVariant, output RTPOutput) bool {
	if variant.isVideoPassthrough != output.CopyVideo {
		return false
	}
	if !output.CopyVideo && output.VideoBitrateKbps > 0 && variant.videoBitrate != output.VideoBitrateKbps {
		// Sharing still saves an encode, but the viewer receives the HLS
		// variant's bitrate rather than the WebRTC-specific override; make that
		// visible instead of silent.
		log.Debugf("transcoder: WebRTC leg %d requests %d kbps but shares the %d kbps HLS variant",
			output.VariantIndex, output.VideoBitrateKbps, variant.videoBitrate)
	}

	return true
}

// sharedHLSFlags mirrors the HLS muxer flags used by the legacy HLS output.
func (t *Transcoder) sharedHLSFlags() []string {
	flags := []string{"program_date_time", "independent_segments"}
	if t.appendToStream {
		flags = append(flags, "append_list")
	}
	if !t.isEvent {
		flags = append(flags, "omit_endlist")
	}

	return flags
}

// teeSelect escapes a stream specifier for a tee slave option. The value
// contains ':' which is the tee option separator, so it must be quoted with
// escaped quotes for av_opt_get_key_value.
func teeSelect(specifier string) string {
	return "\\'" + specifier + "\\'"
}

// teeEscapeColons escapes ':' inside a tee slave option value (URLs). Two
// backslashes are required because the value is parsed twice.
func teeEscapeColons(value string) string {
	return strings.ReplaceAll(value, ":", "\\\\:")
}

func teeOptionString(key, value string) string {
	return key + "=" + value
}

// sharedAudioEncoderFlags renders the single Opus encoder used by every
// WebRTC leg. The Opus stream is an extra output audio stream after the HLS
// variant audio streams, so every encoder option is scoped to that stream
// index: unscoped options would override the HLS variants' AAC copy/encode
// settings and re-encode their audio as Opus.
func (t *Transcoder) sharedAudioEncoderFlags() []string {
	if !sharedAudioCompatible(t.rtpOutputs) {
		return nil
	}
	opusBitrate, opusFrame := t.rtpOutputs[0].opusSettings()
	audioIndex := len(t.variants)
	suffix := ":" + strconv.Itoa(audioIndex)

	return []string{
		ffmpegFlagMap, "0:a:0?",
		"-c:a" + suffix, "libopus",
		"-b:a" + suffix, strconv.Itoa(opusBitrate) + "k",
		"-ar" + suffix, "48000",
		"-ac" + suffix, "2",
		"-application" + suffix, "lowdelay",
		"-frame_duration" + suffix, strconv.Itoa(opusFrame),
	}
}

// getSharedOutputFlags renders the single tee output used when HLS and WebRTC
// share video encodes. Every HLS variant is a single-variant HLS muxer slave;
// each RTP leg selects the already-encoded variant video stream and the shared
// Opus audio stream.
func (t *Transcoder) getSharedOutputFlags(localListenerAddress string) []string {
	flags := make([]string, 0, len(t.variants)*24+64)
	t.sharedMode = true
	defer func() { t.sharedMode = false }()
	for _, variant := range t.variants {
		flags = append(flags, variant.getVariantString(t)...)
	}
	flags = append(flags, t.codec.ExtraArguments()...)
	flags = append(
		flags,
		"-pix_fmt", t.codec.PixelFormat(),
		"-sc_threshold", "0",
		"-max_muxing_queue_size", "400",
	)
	flags = append(flags, t.sharedAudioEncoderFlags()...)

	slaves := make([]string, 0, len(t.variants)+2*len(t.rtpOutputs))
	hlsFlags := strings.Join(t.sharedHLSFlags(), "+")
	segmentIdentifier := t.segmentIdentifier
	if segmentIdentifier == "" {
		segmentIdentifier = shortid.MustGenerate()
	}

	// Each variant's getVariantString maps exactly one video and one audio
	// stream, in that order.
	for index := range t.variants {
		options := []string{
			// HLS writes go over HTTP to Owncast and can occasionally block;
			// a deep FIFO keeps that from stalling the RTP slaves (which are
			// written directly so their packets stay real-time).
			teeOptionString("use_fifo", "1"),
			teeOptionString("fifo_options", "queue_size=10000"),
			teeOptionString("f", "hls"),
			teeOptionString("select", teeSelect(fmt.Sprintf("v:%d,a:%d", index, index))),
			teeOptionString("hls_time", strconv.Itoa(t.currentLatencyLevel.SecondsPerSegment)),
			teeOptionString("hls_list_size", strconv.Itoa(t.currentLatencyLevel.SegmentCount)),
			teeOptionString("hls_flags", hlsFlags),
			teeOptionString("method", "PUT"),
			teeOptionString("hls_segment_filename",
				teeEscapeColons(localListenerAddress+"/"+strconv.Itoa(index)+"/stream-"+segmentIdentifier+"-%d.ts")),
		}
		if t.isEvent {
			options = append(options, teeOptionString("hls_playlist_type", "event"))
		}
		playlistURL := localListenerAddress + "/" + strconv.Itoa(index) + "/stream.m3u8"
		slaves = append(slaves, "["+strings.Join(options, ":")+"]"+playlistURL)
	}

	// Tee select specifiers are stream ordinals within each media type: the
	// shared Opus stream is the Nth audio stream, and each video leg selects
	// its HLS variant's video ordinal.
	opusAudioOrdinal := len(t.variants)
	for _, output := range t.rtpOutputs {
		// Copy legs keep a dedicated RTP output (the legacy, known-good path);
		// only encoded legs share the tee video stream. The source packets are
		// still copied, not re-encoded.
		if !output.CopyVideo {
			videoVariant, ok := t.sharedVideoVariantFor(output)
			if !ok {
				continue
			}
			videoSlaveOptions := []string{
				teeOptionString("select", teeSelect("v:"+strconv.Itoa(videoVariant))),
				teeOptionString("f", "rtp"),
				teeOptionString("payload_type", strconv.Itoa(output.VideoPayloadType)),
			}
			slaves = append(slaves, "["+strings.Join(videoSlaveOptions, ":")+"]"+withRTPPacketSize(output.VideoURL))
		}

		audioSlaveOptions := []string{
			teeOptionString("select", teeSelect("a:"+strconv.Itoa(opusAudioOrdinal))),
			teeOptionString("f", "rtp"),
			teeOptionString("payload_type", strconv.Itoa(output.AudioPayloadType)),
		}
		slaves = append(slaves, "["+strings.Join(audioSlaveOptions, ":")+"]"+withRTPPacketSize(output.AudioURL))
	}

	flags = append(flags, "-f", "tee", strings.Join(slaves, "|"))

	// Dedicated copy video legs: keep the legacy RTP output shape for sources
	// whose copied bitstream browsers are known to accept.
	for _, output := range t.rtpOutputs {
		if !output.CopyVideo {
			continue
		}
		// h264_mp4toannexb is a no-op for already-Annex-B sources and converts
		// FLV/AVCC copy legs; dump_extra then injects SPS/PPS on keyframes so
		// WebRTC copy legs are decodable for mid-stream viewers.
		copyBitstreamFilter := "h264_mp4toannexb,dump_extra=freq=keyframe"
		flags = append(
			flags,
			ffmpegFlagMap, "0:v:0",
			"-c:v", "copy",
			"-bsf:v", copyBitstreamFilter,
			"-f", "rtp",
			"-payload_type", strconv.Itoa(output.VideoPayloadType),
			withRTPPacketSize(output.VideoURL),
		)
	}

	return flags
}

// audioBitrateKbpsFor returns the audio bitrate a variant declares, in kbps.
// Passthrough variants copy the source audio, so 128 is assumed.
func audioBitrateKbpsFor(variant HLSVariant) int {
	if variant.isAudioPassthrough || variant.audioBitrate == "" {
		return 128
	}

	value := strings.TrimSuffix(variant.audioBitrate, "k")
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 128
	}

	return parsed
}

// writeMasterPlaylist writes the HLS master playlist for the tee output. With
// one HLS muxer per variant, ffmpeg only writes variant playlists, so the
// master is assembled here from the configured variants.
func (t *Transcoder) writeMasterPlaylist() {
	if !t.sharedOutputEnabled() {
		return
	}
	var builder strings.Builder
	builder.WriteString("#EXTM3U\n")
	builder.WriteString("#EXT-X-VERSION:3\n")
	builder.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n")
	// A passthrough variant carries the source, which is at least as expensive as
	// the most expensive rendition derived from it; declaring it any lower made
	// ABR choose it as the "cheapest" option.
	passthroughFloorKbps := 0
	for _, variant := range t.variants {
		if maxKbps := variant.getMaxVideoBitrate(); maxKbps > passthroughFloorKbps {
			passthroughFloorKbps = maxKbps
		}
	}

	for _, variant := range t.variants {
		// HLS requires BANDWIDTH to be the peak, and players pick with it, so the
		// declared value follows the encoder ceiling rather than the average.
		peakKbps := variant.getMaxVideoBitrate()
		peakKbps += audioBitrateKbpsFor(variant)
		if peakKbps < 1000 {
			peakKbps = 1000
		}

		averageKbps := variant.getAllocatedVideoBitrate() + audioBitrateKbpsFor(variant)
		if averageKbps < 1000 {
			averageKbps = 1000
		}

		if variant.isVideoPassthrough {
			if passthroughFloorKbps > 0 {
				peakKbps = passthroughFloorKbps + audioBitrateKbpsFor(variant)
			}
			averageKbps = peakKbps
		}

		attributes := []string{
			"BANDWIDTH=" + strconv.Itoa(peakKbps*1000),
			"AVERAGE-BANDWIDTH=" + strconv.Itoa(averageKbps*1000),
		}
		// A passthrough rendition is not scaled, so videoSize only restates the
		// configured scaler inputs: advertising it claimed a resolution the copied
		// segments do not have (854x480 announced while the frames were 1280x720).
		if !variant.isVideoPassthrough && variant.videoSize.Width > 0 && variant.videoSize.Height > 0 {
			attributes = append(attributes, fmt.Sprintf("RESOLUTION=%dx%d", variant.videoSize.Width, variant.videoSize.Height))
		}
		if variant.framerate > 0 {
			attributes = append(attributes, "FRAME-RATE="+strconv.Itoa(variant.framerate))
		}
		attributes = append(attributes, "CODECS=\""+t.masterPlaylistCodecs(variant)+"\"")
		builder.WriteString("#EXT-X-STREAM-INF:" + strings.Join(attributes, ",") + "\n")
		builder.WriteString(strconv.Itoa(variant.index) + "/stream.m3u8\n")
	}
	if err := os.WriteFile(path.Join(config.HLSStoragePath, "stream.m3u8"), []byte(builder.String()), 0o600); err != nil {
		log.Errorf("transcoder: could not write HLS master playlist: %v", err)
	}
}

// H.264 profile_idc bytes for the master playlist CODECS attribute.
const (
	h264ProfileHexBaseline = "42"
	h264ProfileHexMain     = "4d"
	h264ProfileHexHigh     = "64"
)

// masterPlaylistCodecs renders the CODECS attribute for one variant from the
// configured encoder profile and level, so a High or Baseline stream is not
// advertised as Main 3.1. With "auto" the level is derived from the variant's
// resolution and frame rate, which is what hardware encoders pick.
func (t *Transcoder) masterPlaylistCodecs(variant HLSVariant) string {
	profile := strings.ToLower(strings.TrimSpace(t.sharedH264Profile))
	profileHex := h264ProfileHexMain
	switch profile {
	case h264ProfileBaseline:
		profileHex = h264ProfileHexBaseline
	case h264ProfileHigh:
		profileHex = h264ProfileHexHigh
	}
	levelHex, ok := parseSharedH264LevelHex(t.sharedH264Level)
	if !ok {
		levelHex = estimatedH264LevelHex(variant.videoSize.Width, variant.videoSize.Height, variant.framerate)
		if levelHex == "" {
			// A passthrough variant has no configured size or frame rate; the
			// source is at least as large as the biggest transcode variant.
			levelHex = t.largestVariantLevelHex()
		}
	}

	return "avc1." + profileHex + "00" + levelHex + ",mp4a.40.2"
}

// largestVariantLevelHex returns the highest level required by any variant with
// a known size and frame rate.
func (t *Transcoder) largestVariantLevelHex() string {
	best := "1f"
	bestRank := -1
	for _, variant := range t.variants {
		hex := estimatedH264LevelHex(variant.videoSize.Width, variant.videoSize.Height, variant.framerate)
		if hex == "" {
			continue
		}
		if rank := levelHexRank(hex); rank > bestRank {
			best, bestRank = hex, rank
		}
	}

	return best
}

// levelHexRank orders the level hex strings produced by estimatedH264LevelHex.
func levelHexRank(hex string) int {
	switch hex {
	case "28":
		return 40
	case "29":
		return 41
	case "2a":
		return 42
	case "32":
		return 50
	case "33":
		return 51
	case "34":
		return 52
	default:
		return 0
	}
}

// estimatedH264LevelHex picks the smallest H.264 level that can carry the given
// resolution and frame rate, using the level's macroblock limits.
func estimatedH264LevelHex(width, height, fps int) string {
	if width <= 0 || height <= 0 || fps <= 0 {
		return ""
	}
	mbWidth := (width + 15) / 16
	mbHeight := (height + 15) / 16
	mbPerFrame := mbWidth * mbHeight
	mbPerSecond := mbPerFrame * fps
	type levelLimit struct {
		levelHex      string
		maxMBPerSec   int
		maxMBPerFrame int
	}
	limits := []levelLimit{
		{"28", 245760, 8192},   // 4.0
		{"29", 245760, 8192},   // 4.1
		{"2a", 522240, 8704},   // 4.2
		{"32", 589824, 22080},  // 5.0
		{"33", 983040, 36864},  // 5.1
		{"34", 2073600, 36864}, // 5.2
	}
	for _, limit := range limits {
		if mbPerSecond <= limit.maxMBPerSec && mbPerFrame <= limit.maxMBPerFrame {
			return limit.levelHex
		}
	}

	return "34"
}

// parseSharedH264LevelHex converts a configured H.264 level ("4.0", "5.1",
// "auto") into the two hex digits used in the CODECS attribute.
func parseSharedH264LevelHex(level string) (string, bool) {
	level = strings.TrimSpace(strings.ToLower(level))
	if level == "" || level == "auto" {
		return "", false
	}
	parts := strings.SplitN(level, ".", 2)
	major, err := strconv.Atoi(parts[0])
	if err != nil || major <= 0 || major > 6 {
		return "", false
	}
	minor := 0
	if len(parts) == 2 {
		parsed, minorErr := strconv.Atoi(parts[1])
		if minorErr != nil || parsed < 0 || parsed > 2 {
			return "", false
		}
		minor = parsed
	}

	return fmt.Sprintf("%02x", major*10+minor), true
}
