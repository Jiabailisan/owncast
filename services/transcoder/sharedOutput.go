package transcoder

import (
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/owncast/owncast/config"

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
		if output.VariantIndex < 0 || output.VariantIndex >= len(t.variants) {
			return false
		}
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
		videoSlaveOptions := []string{
			"bsfs/v=dump_extra=freq=keyframe",
			teeOptionString("select", teeSelect("v:"+strconv.Itoa(output.VariantIndex))),
			teeOptionString("f", "rtp"),
			teeOptionString("payload_type", strconv.Itoa(output.VideoPayloadType)),
		}
		slaves = append(slaves, "["+strings.Join(videoSlaveOptions, ":")+"]"+withRTPPacketSize(output.VideoURL))

		audioSlaveOptions := []string{
			teeOptionString("select", teeSelect("a:"+strconv.Itoa(opusAudioOrdinal))),
			teeOptionString("f", "rtp"),
			teeOptionString("payload_type", strconv.Itoa(output.AudioPayloadType)),
		}
		slaves = append(slaves, "["+strings.Join(audioSlaveOptions, ":")+"]"+withRTPPacketSize(output.AudioURL))
	}

	flags = append(flags, "-f", "tee", strings.Join(slaves, "|"))

	return flags
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
	for _, variant := range t.variants {
		bandwidth := (variant.videoBitrate + 128) * 1000
		if bandwidth < 1000*1000 {
			bandwidth = 1000 * 1000
		}
		attributes := []string{
			"BANDWIDTH=" + strconv.Itoa(bandwidth),
			"AVERAGE-BANDWIDTH=" + strconv.Itoa(bandwidth),
		}
		if variant.videoSize.Width > 0 && variant.videoSize.Height > 0 {
			attributes = append(attributes, fmt.Sprintf("RESOLUTION=%dx%d", variant.videoSize.Width, variant.videoSize.Height))
		}
		if variant.framerate > 0 {
			attributes = append(attributes, "FRAME-RATE="+strconv.Itoa(variant.framerate))
		}
		attributes = append(attributes, "CODECS=\"avc1.4d401f,mp4a.40.2\"")
		builder.WriteString("#EXT-X-STREAM-INF:" + strings.Join(attributes, ",") + "\n")
		builder.WriteString(strconv.Itoa(variant.index) + "/stream.m3u8\n")
	}
	_ = os.WriteFile(path.Join(config.HLSStoragePath, "stream.m3u8"), []byte(builder.String()), 0o600)
}
