package transcoder

import (
	"fmt"
	"math"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/owncast/owncast/config"

	log "github.com/sirupsen/logrus"
	"github.com/teris-io/shortid"
)

// sharedOutputEnabled reports whether at least one WebRTC RTP leg can reuse an
// HLS encode. It requires a shared-capable codec, no S3 output, legs that can
// share one Opus encode, and at least one leg whose whole output plan matches an
// HLS variant. Isolated-process configurations keep the legacy layout.
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
	if !allLegsShareOpusSettings(t.rtpOutputs) {
		return false
	}

	return len(t.sharedPlan()) > 0
}

// sharedPlan maps each RTP leg that may reuse an HLS encode to the index of the
// variant whose encode feeds it. Legs absent from the map get their own encode
// in the same FFmpeg process (see getSharedOutputFlags).
//
// Reuse is allowed only when the HLS encode already satisfies the leg's whole
// output plan: resolution, frame rate, rate-control target, H.264 profile, GOP
// and B-frame setting. A leg that asks for something the HLS encode does not
// produce must never be fed that encode, because the viewer would silently
// receive a different rendition: the field report was a 1440p passthrough tier
// served from the 1080p rendition's encode, at that rendition's 5 s GOP.
func (t *Transcoder) sharedPlan() map[int]int {
	plan := make(map[int]int, len(t.rtpOutputs))
	for index, output := range t.rtpOutputs {
		if output.CopyVideo {
			// Copy legs keep the dedicated copy output: there is no encode to share.
			continue
		}
		variantIndex, ok := t.sharedVideoVariantFor(output)
		if !ok {
			continue
		}
		plan[index] = variantIndex
	}

	return plan
}

// sharedVideoVariantFor maps a WebRTC leg to the HLS variant whose encoded
// stream it can reuse. The variant matching the leg's configured VariantIndex
// is preferred (even when earlier variants disabled HLS and shifted HLS
// indices), then the same positional index, then any variant with a matching
// output plan. The false return means the leg cannot be shared and must get its
// own encode.
func (t *Transcoder) sharedVideoVariantFor(output RTPOutput) (int, bool) {
	for _, variant := range t.variants {
		if variant.hasConfigID && variant.configIndex == output.VariantIndex && t.variantMatchesLeg(variant, output) {
			return variant.index, true
		}
	}
	if output.VariantIndex >= 0 && output.VariantIndex < len(t.variants) {
		if t.variantMatchesLeg(t.variants[output.VariantIndex], output) {
			return t.variants[output.VariantIndex].index, true
		}
	}
	for _, variant := range t.variants {
		if t.variantMatchesLeg(variant, output) {
			return variant.index, true
		}
	}

	return 0, false
}

// variantMatchesLeg reports whether this variant's encode satisfies the leg's
// entire output plan.
//
// The passthrough flag must agree: a transcode leg must never be fed the raw
// source stream, and a copy leg must never be fed an encoded one. For a
// transcode leg every parameter the encoder writes has to match, because the
// tee hands the viewer the HLS variant's bitstream as-is:
//
//   - resolution: the leg's scale expression is what FFmpeg is told; the variant
//     renders the same string when it scales, and "" when neither scales;
//   - frame rate: one constant rate per encode;
//   - rate control: the shared encode is pinned to the variant's video bitrate,
//     so the leg's target must be that same number;
//   - GOP: the shared encode forces a keyframe every HLS segment, so a leg that
//     asks for a different keyframe interval cannot use it;
//   - profile: the shared encode writes the configured WebRTC profile;
//   - B-frames: the shared encode is rendered with bframes=0 (see
//     SharedVariantFlags), which is what every transcode leg asks for.
func (t *Transcoder) variantMatchesLeg(variant HLSVariant, output RTPOutput) bool {
	if variant.isVideoPassthrough != output.CopyVideo {
		return false
	}
	if output.CopyVideo {
		// A copy leg forwards the source bitstream, which is exactly what a
		// passthrough variant does.
		return true
	}
	if variant.videoSize.getString() != output.Scale {
		return false
	}
	if variant.framerate != output.Framerate {
		return false
	}
	// The shared encode is pinned to the variant's video bitrate minus the audio
	// room (the HLS rate-control rule, frozen by the golden argv). A leg whose
	// target is that same video rate therefore cannot be fed this encode without
	// silently losing rate: only an equal effective rate shares.
	if variant.getAllocatedVideoBitrate() != output.VideoBitrateKbps {
		return false
	}
	if variant.framerate*t.currentLatencyLevel.SecondsPerSegment != output.GOPFrames {
		return false
	}

	return normalizeH264Profile(output.H264Profile) == normalizeSharedH264Profile(variant.h264Profile)
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
//
// The specifier must be audio-relative (":a:N"), not the flat output index
// (":N"). With two variants the flat stream list is v:0, a:0, v:1, a:1, opus, so
// a flat ":2" addresses a video stream: FFmpeg dropped -application and
// -frame_duration with "Codec AVOption ... has not been used for any stream",
// which silently disabled the low-delay and frame-size settings WebRTC relies on.
func (t *Transcoder) sharedAudioEncoderFlags() []string {
	if !allLegsShareOpusSettings(t.rtpOutputs) {
		return nil
	}
	opusBitrate, opusFrame := t.rtpOutputs[0].opusSettings()
	audioIndex := len(t.variants)
	suffix := ":" + strconv.Itoa(audioIndex)

	return []string{
		ffmpegFlagMap, "0:a:0?",
		"-c:a" + suffix, "libopus",
		"-b:a" + suffix, strconv.Itoa(opusBitrate) + "k",
		"-ar:a" + suffix, "48000",
		"-ac:a" + suffix, "2",
		"-application:a" + suffix, "lowdelay",
		"-frame_duration:a" + suffix, strconv.Itoa(opusFrame),
	}
}

// getSharedOutputFlags renders the single tee output used when HLS and WebRTC
// share video encodes. Every HLS variant is a single-variant HLS muxer slave;
// each shared RTP leg selects the already-encoded variant video stream and the
// shared Opus audio stream. Legs whose plan no HLS encode satisfies, and the
// copy legs, are rendered as separate outputs after the tee in this same
// process.
func (t *Transcoder) getSharedOutputFlags(localListenerAddress string) []string {
	plan := t.sharedPlan()
	t.markSharedVariants(plan)
	defer t.clearSharedVariants()

	flags := make([]string, 0, len(t.variants)*24+64)
	flags = t.appendSharedVariantEncoders(flags)
	slaves := t.sharedTeeSlaves(localListenerAddress, plan)
	flags = append(flags, "-f", "tee", strings.Join(slaves, "|"))
	flags = t.appendIndependentLegOutputs(flags, plan)
	flags = t.appendCopyLegOutputs(flags)

	return flags
}

// markSharedVariants flags the variants whose encode a leg actually reuses. Only
// those get the WebRTC-compatible encoder constraints; a variant that no leg
// shares keeps the plain HLS encode.
func (t *Transcoder) markSharedVariants(plan map[int]int) {
	t.sharedMode = true
	for legIndex := range plan {
		t.variants[plan[legIndex]].sharedEncode = true
	}
}

// clearSharedVariants resets the shared-mode markers once the flags are built.
func (t *Transcoder) clearSharedVariants() {
	t.sharedMode = false
	for index := range t.variants {
		t.variants[index].sharedEncode = false
	}
}

// appendSharedVariantEncoders renders the per-variant encoders and the
// muxer-wide options that precede the tee output.
func (t *Transcoder) appendSharedVariantEncoders(flags []string) []string {
	for _, variant := range t.variants {
		flags = append(flags, variant.getVariantString(t)...)
	}
	flags = append(flags, t.codec.ExtraArguments()...)
	flags = append(flags, "-pix_fmt", t.codec.PixelFormat())
	flags = append(flags, scThresholdFlags(t.codec)...)
	flags = append(flags, "-max_muxing_queue_size", "400")
	if !t.sourceVideoOnly {
		flags = append(flags, t.sharedAudioEncoderFlags()...)
	}

	return flags
}

// sharedTeeSlaves renders the tee slave list: one HLS slave per variant, the
// shared video RTP target for every leg that reuses an encode, and the shared
// audio RTP target for every leg (including independent transcode and copy
// legs) unless the source is video-only.
func (t *Transcoder) sharedTeeSlaves(localListenerAddress string, plan map[int]int) []string {
	slaves := make([]string, 0, len(t.variants)+2*len(t.rtpOutputs))
	hlsFlags := strings.Join(t.sharedHLSFlags(), "+")
	segmentIdentifier := t.segmentIdentifier
	if segmentIdentifier == "" {
		segmentIdentifier = shortid.MustGenerate()
	}
	for index := range t.variants {
		slaves = append(slaves, t.sharedHLSSlave(localListenerAddress, index, hlsFlags, segmentIdentifier))
	}

	// Tee select specifiers are stream ordinals within each media type: the
	// shared Opus stream is the Nth audio stream, and each shared video leg
	// selects its HLS variant's video ordinal.
	opusAudioOrdinal := len(t.variants)
	for legIndex, output := range t.rtpOutputs {
		if !output.CopyVideo {
			if videoVariant, ok := plan[legIndex]; ok {
				videoSlaveOptions := []string{
					teeOptionString("select", teeSelect("v:"+strconv.Itoa(videoVariant))),
					teeOptionString("f", "rtp"),
					teeOptionString("payload_type", strconv.Itoa(output.VideoPayloadType)),
				}
				slaves = append(slaves, "["+strings.Join(videoSlaveOptions, ":")+"]"+withRTPPacketSize(output.VideoURL))
			}
			// Legs absent from plan get their own video encode after the tee
			// (see appendIndependentLegOutputs), while still receiving the
			// shared Opus audio stream below.
		}

		if t.sourceVideoOnly {
			continue
		}
		audioSlaveOptions := []string{
			teeOptionString("select", teeSelect("a:"+strconv.Itoa(opusAudioOrdinal))),
			teeOptionString("f", "rtp"),
			teeOptionString("payload_type", strconv.Itoa(output.AudioPayloadType)),
		}
		slaves = append(slaves, "["+strings.Join(audioSlaveOptions, ":")+"]"+withRTPPacketSize(output.AudioURL))
	}

	return slaves
}

// sharedHLSSlave renders the HLS muxer slave for one variant.
func (t *Transcoder) sharedHLSSlave(localListenerAddress string, index int, hlsFlags, segmentIdentifier string) string {
	selectStreams := fmt.Sprintf("v:%d,a:%d", index, index)
	if t.sourceVideoOnly {
		selectStreams = fmt.Sprintf("v:%d", index)
	}
	options := []string{
		// HLS writes go over HTTP to Owncast and can occasionally block; a deep
		// FIFO keeps that from stalling the RTP slaves (which are written
		// directly so their packets stay real-time).
		teeOptionString("use_fifo", "1"),
		teeOptionString("fifo_options", "queue_size=10000"),
		teeOptionString("f", "hls"),
		teeOptionString("select", teeSelect(selectStreams)),
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

	return "[" + strings.Join(options, ":") + "]" + playlistURL
}

// appendIndependentLegOutputs renders one independent encode for every leg whose
// plan no HLS encode satisfies, in this same FFmpeg process, so the viewer still
// gets the rendition the plan describes instead of a reused one that does not
// match it.
func (t *Transcoder) appendIndependentLegOutputs(flags []string, plan map[int]int) []string {
	for legIndex, output := range t.rtpOutputs {
		if output.CopyVideo {
			continue
		}
		if _, shared := plan[legIndex]; shared {
			continue
		}
		flags = append(flags, t.rtpVideoOutputFlags(output)...)
	}

	return flags
}

// appendCopyLegOutputs renders the dedicated copy legs: the legacy RTP output
// shape for sources whose copied bitstream browsers are known to accept.
func (t *Transcoder) appendCopyLegOutputs(flags []string) []string {
	// h264_mp4toannexb is a no-op for already-Annex-B sources and converts
	// FLV/AVCC copy legs; dump_extra then injects SPS/PPS on keyframes so
	// WebRTC copy legs are decodable for mid-stream viewers.
	const copyBitstreamFilter = "h264_mp4toannexb,dump_extra=freq=keyframe"
	for _, output := range t.rtpOutputs {
		if !output.CopyVideo {
			continue
		}
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
			effectiveFloor := passthroughFloorKbps
			if t.sourceVideoBitrateKbps > effectiveFloor {
				effectiveFloor = t.sourceVideoBitrateKbps
			}
			if effectiveFloor > 0 {
				peakKbps = effectiveFloor + audioBitrateKbpsFor(variant)
			}
			averageKbps = peakKbps
		}

		attributes := []string{
			"BANDWIDTH=" + strconv.Itoa(peakKbps*1000),
			"AVERAGE-BANDWIDTH=" + strconv.Itoa(averageKbps*1000),
		}
		// A passthrough rendition is not scaled, so videoSize only restates the
		// configured scaler inputs; emit the actual probed source resolution
		// instead when available so HLS players (and ABR) recognize its size.
		if variant.isVideoPassthrough {
			if t.sourceWidth > 0 && t.sourceHeight > 0 {
				attributes = append(attributes, fmt.Sprintf("RESOLUTION=%dx%d", t.sourceWidth, t.sourceHeight))
			}
		} else if variant.videoSize.Width > 0 && variant.videoSize.Height > 0 {
			attributes = append(attributes, fmt.Sprintf("RESOLUTION=%dx%d", variant.videoSize.Width, variant.videoSize.Height))
		}
		if variant.framerate > 0 {
			attributes = append(attributes, "FRAME-RATE="+strconv.Itoa(variant.framerate))
		} else if variant.isVideoPassthrough && t.sourceFPS > 0 {
			attributes = append(attributes, "FRAME-RATE="+strconv.Itoa(int(math.Round(t.sourceFPS))))
		}
		attributes = append(attributes, "CODECS=\""+t.masterPlaylistCodecs(variant)+"\"")
		builder.WriteString("#EXT-X-STREAM-INF:" + strings.Join(attributes, ",") + "\n")
		builder.WriteString(strconv.Itoa(variant.index) + "/stream.m3u8\n")
	}
	masterPath := path.Join(config.HLSStoragePath, "stream.m3u8")
	if err := os.WriteFile(masterPath, []byte(builder.String()), 0o600); err != nil {
		log.Errorf("transcoder: could not write HLS master playlist: %v", err)
		return
	}
	if t.onMasterPlaylistWritten != nil {
		t.onMasterPlaylistWritten(masterPath)
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
	if variant.isVideoPassthrough && strings.TrimSpace(t.sourceVideoProfile) != "" {
		profile = strings.ToLower(strings.TrimSpace(t.sourceVideoProfile))
	}
	profileHex := h264ProfileHexMain
	switch profile {
	case h264ProfileBaseline, "constrained-baseline":
		profileHex = h264ProfileHexBaseline
	case h264ProfileHigh, "constrained-high":
		profileHex = h264ProfileHexHigh
	}
	var levelHex string
	var ok bool
	if variant.isVideoPassthrough {
		levelHex, ok = parseSharedH264LevelHex(t.sourceVideoLevel)
		if !ok && t.sourceWidth > 0 && t.sourceHeight > 0 && t.sourceFPS > 0 {
			levelHex = estimatedH264LevelHex(t.sourceWidth, t.sourceHeight, int(math.Round(t.sourceFPS)))
			ok = levelHex != ""
		}
	}
	if !ok {
		levelHex, ok = parseSharedH264LevelHex(t.sharedH264Level)
	}
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
