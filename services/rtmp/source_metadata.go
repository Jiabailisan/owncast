package rtmp

// Source metadata for RTMP ingest.
//
// The FLV/AVC inspector runs on the same tags the packet pump forwards, so RTMP
// answers the same questions about a source as SRT does: H.264 profile, level,
// resolution, frame rate, whether it uses B-frames, and a measured video
// bitrate. onMetaData is still honoured, but only for what the bitstream cannot
// provide (the encoder name) or as a fallback when the probe has not seen the
// value yet. Without this, an RTMP passthrough leg could only ever be delivered
// as an "unverified copy", which cannot be checked against the same rules the
// SRT path applies.

import (
	"fmt"
	"math"
	"time"

	"github.com/nareix/joy5/av"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/ingest/flv"
)

// inspectPacket feeds one demuxed packet to the source inspector. It runs on the
// packet pump, after the packet is on its way to the media pipe, so it never
// delays or reorders the media path.
func (s *session) inspectPacket(pkt av.Packet) {
	if s.inspector == nil {
		return
	}
	// RTMP timestamps are 32-bit milliseconds, so the media clock wraps after
	// about 49 days; truncating to that width is what the container does.
	timeMs := uint32(pkt.Time.Milliseconds() & 0xFFFFFFFF) //nolint:gosec // intentional 32-bit media clock

	switch pkt.Type {
	case av.H264DecoderConfig:
		s.inspector.FeedVideo(flv.VideoFormatAVC, flv.AVCPacketSequenceHeader, pkt.Data, timeMs)
	case av.H264, av.H264SPSPPSNALU:
		s.inspector.FeedVideo(flv.VideoFormatAVC, flv.AVCPacketNALU, pkt.Data, timeMs)
	case av.AACDecoderConfig:
		s.inspector.FeedAudio(flv.SoundFormatAAC, flv.AACPacketSequenceHead, pkt.Data, timeMs)
	case av.AAC:
		s.inspector.FeedAudio(flv.SoundFormatAAC, flv.AACPacketRaw, pkt.Data, timeMs)
	}

	// Publish from the packet path as soon as the verdict exists, instead of
	// waiting for the next statistics tick.
	s.publishSourceInfoIfDecided()
}

// SourceInfoReady is closed once the B-frame verdict has been published as the
// session broadcaster, so a WebRTC copy decision does not have to wait for the
// next statistics tick.
func (s *session) SourceInfoReady() <-chan struct{} { return s.sourceReady }

// publishSourceInfoIfDecided publishes from the packet pump as soon as there is
// something new to publish: the B-frame verdict, and then the measured video
// bitrate once its window has enough samples. Everything else waits for the
// one-second statistics tick, so the per-packet cost is two atomic loads once
// both have been published.
func (s *session) publishSourceInfoIfDecided() {
	if s.inspector == nil {
		return
	}
	if !s.sourceDecided.Load() {
		if !s.inspector.Decided() {
			return
		}
		s.refreshSourceMetadata()

		return
	}
	if s.bitratePublished.Load() {
		return
	}
	if !s.inspector.Snapshot().VideoBitrateValid {
		return
	}
	s.bitratePublished.Store(true)
	s.refreshSourceMetadata()
}

// refreshSourceMetadata publishes the current inspected properties merged with
// whatever onMetaData declared.
func (s *session) refreshSourceMetadata() {
	if s.inspector == nil {
		return
	}
	s.publishSourceMetadata(s.inspector.Snapshot())
}

// setDeclaredDetails records the onMetaData claim for this session and republishes
// the merged view, so a metadata tag that arrives after the first inspection
// still lands on the right session.
func (s *session) setDeclaredDetails(details models.InboundStreamDetails) {
	s.sourceMu.Lock()
	s.declared = details
	s.sourceMu.Unlock()

	s.refreshSourceMetadata()
}

// publishSourceMetadata publishes one snapshot of the inspected properties.
// Publishing is deduplicated and a published B-frame verdict is sticky: the two
// publishers (packet pump and statistics tick) can interleave, and a copy
// decision may already be waking up on the verdict.
func (s *session) publishSourceMetadata(info flv.Info) {
	details := s.mergeDetails(info)
	if details.VideoCodec == "" && details.AudioCodec == "" && details.Width == 0 {
		return
	}

	key := fmt.Sprintf("%s|%s|%s|%s|%d|%d|%t|%t|%d|%d|%d|%d",
		details.VideoCodec, details.AudioCodec, details.VideoProfile, details.VideoLevel,
		details.Width, details.Height, details.HasBFrames != nil,
		details.HasBFrames != nil && *details.HasBFrames,
		details.VideoBitrate, details.AudioBitrate, details.AudioSampleRate, details.AudioChannels)

	s.sourceMu.Lock()
	defer s.sourceMu.Unlock()

	if key == s.lastSourceKey {
		return
	}
	if s.publishedBFrames && (details.HasBFrames == nil || !*details.HasBFrames) {
		return
	}
	if s.sourceDecided.Load() && details.HasBFrames == nil {
		return
	}
	s.lastSourceKey = key

	s.SetBroadcaster(&models.Broadcaster{
		RemoteAddr:    remoteAddrString(s),
		Time:          time.Now(),
		StreamDetails: details,
	})

	if details.HasBFrames != nil {
		s.publishedBFrames = *details.HasBFrames
		s.sourceReadyOnce.Do(func() { close(s.sourceReady) })
		s.sourceDecided.Store(true)
	}
}

// mergeDetails combines the inspected properties with the onMetaData claim. The
// bitstream wins wherever the probe has a value; the claim fills the rest (and
// always provides the encoder name).
func (s *session) mergeDetails(info flv.Info) models.InboundStreamDetails {
	s.sourceMu.Lock()
	declared := s.declared
	s.sourceMu.Unlock()

	details := models.InboundStreamDetails{
		Encoder:         declared.Encoder,
		VideoProfile:    info.Profile,
		VideoLevel:      info.Level,
		AudioProfile:    info.AudioProfile,
		AudioSampleRate: info.AudioSampleRate,
		AudioChannels:   info.AudioChannels,
		VideoOnly:       declared.VideoOnly,
	}

	details.Width = info.Width
	if details.Width == 0 {
		details.Width = declared.Width
	}
	details.Height = info.Height
	if details.Height == 0 {
		details.Height = declared.Height
	}
	details.VideoCodec = videoCodecDisplayName(info.VideoCodec)
	if details.VideoCodec == "" {
		details.VideoCodec = declared.VideoCodec
	}
	details.AudioCodec = audioCodecDisplayName(info.AudioCodec)
	if details.AudioCodec == "" {
		details.AudioCodec = declared.AudioCodec
	}

	if info.FPSValid {
		details.VideoFramerate = float32(info.FPS)
	} else {
		details.VideoFramerate = declared.VideoFramerate
	}

	// A measured bitrate always beats the encoder's claim: onMetaData reports
	// what the encoder was configured to do, not what the stream carries.
	if info.VideoBitrateValid {
		details.VideoBitrate = int(math.Round(info.VideoBitrateKbps))
		details.VideoBitrateSource = models.VideoBitrateMeasured
	} else if declared.VideoBitrate > 0 {
		details.VideoBitrate = declared.VideoBitrate
		details.VideoBitrateSource = models.VideoBitrateDeclared
	}
	if info.AudioBitrateValid {
		details.AudioBitrate = int(math.Round(info.AudioBitrateKbps))
	} else {
		details.AudioBitrate = declared.AudioBitrate
	}

	if info.BFrameChecked {
		hasBFrames := info.HasBFrames
		details.HasBFrames = &hasBFrames
	}

	return details
}

// videoCodecDisplayName converts the inspector's codec identifiers to the
// display names the existing metadata path uses.
func videoCodecDisplayName(codec string) string {
	switch codec {
	case "h264":
		return "H.264"
	case "hevc":
		return "H.265"
	default:
		return ""
	}
}

func audioCodecDisplayName(codec string) string {
	switch codec {
	case "aac":
		return "AAC"
	case "mp3":
		return "MP3"
	case "pcm":
		return "PCM"
	case "speex":
		return "Speex"
	default:
		return ""
	}
}

// remoteAddrString renders the session's remote address, tolerating a session
// without one.
func remoteAddrString(s *session) string {
	if addr := s.RemoteAddr(); addr != nil {
		return addr.String()
	}

	return ""
}
