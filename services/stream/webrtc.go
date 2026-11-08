package stream

import (
	"math"

	log "github.com/sirupsen/logrus"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/transcoder"
)

// Default video fallbacks used when a variant is a copy variant and therefore
// carries no encoder settings of its own.
const (
	defaultWebRTCVideoBitrateKbps = 2500
	defaultWebRTCFrameRate        = 30
	minimumWebRTCGOPFrames        = 1
)

// WebRTCMediaLeg is the loopback RTP leg contract exposed by the WebRTC
// delivery service. One leg carries one video and one audio RTP stream.
type WebRTCMediaLeg interface {
	VideoURL() string
	AudioURL() string
	Close() error
}

// WebRTCProvider is the subset of the WebRTC delivery service the stream
// lifecycle needs. main.go injects an adapter over *webrtc.Service so the
// stream package does not depend on the Pion implementation.
type WebRTCProvider interface {
	// SetVariants publishes the variants WebRTC delivery should expose. The
	// slice position is the variant index used by WHEP and the FFmpeg plan.
	SetVariants(variants []models.StreamOutputVariant) error
	// OpenMediaLeg allocates the Go-owned loopback sockets for a variant and
	// starts the RTP forwarder.
	OpenMediaLeg(variantIndex int) (WebRTCMediaLeg, error)
	// CloseMediaLeg tears the variant leg down.
	CloseMediaLeg(variantIndex int) error
}

// effectiveWebRTCVariants resolves the output variants WebRTC should deliver.
// When the operator enabled WebRTC globally but did not map any output variant
// to it, every HLS variant is exposed over WebRTC too (documented behavior so
// "enable WebRTC" works without a second configuration step). Explicit
// per-variant mappings always win. The returned slice preserves the original
// variant positions so WHEP variant indices match the configuration.
func effectiveWebRTCVariants(variants []models.StreamOutputVariant) []models.StreamOutputVariant {
	for _, variant := range variants {
		if variant.WebRTCEnabled() {
			full := make([]models.StreamOutputVariant, len(variants))
			copy(full, variants)
			return full
		}
	}

	full := make([]models.StreamOutputVariant, len(variants))
	for index, variant := range variants {
		copied := variant
		if copied.HLSEnabled() {
			if copied.Outputs == nil {
				copied.Outputs = &models.VariantOutputs{}
			}
			copied.Outputs.WebRTC = &models.ProtocolOutputSettings{
				Enabled: models.BoolPtr(true),
				Mode:    models.OutputModeInherit,
			}
		}
		full[index] = copied
	}
	return full
}

// webrtcGOPFrames converts the configured keyframe interval to a frame count
// for the encoder. Copy variants have no encoder settings; the source GOP then
// bounds the join latency and is reported as a warning.
func webrtcGOPFrames(keyframeIntervalSeconds float64, framerate int) int {
	if framerate <= 0 {
		framerate = defaultWebRTCFrameRate
	}
	if keyframeIntervalSeconds <= 0 {
		keyframeIntervalSeconds = 1
	}
	gop := int(math.Round(keyframeIntervalSeconds * float64(framerate)))
	if gop < minimumWebRTCGOPFrames {
		gop = minimumWebRTCGOPFrames
	}
	return gop
}

// configureWebRTC prepares the per-variant WebRTC media legs for the stream
// that is starting and returns the RTP outputs the transcoder must produce.
// Failure is non-fatal: the HLS stream continues and the failure is logged.
func (s *Service) configureWebRTC(variants []models.StreamOutputVariant) []transcoder.RTPOutput {
	if s.webRTC == nil {
		return nil
	}

	webrtcVariants := effectiveWebRTCVariants(variants)
	hasWebRTC := false
	for index := range webrtcVariants {
		if webrtcVariants[index].WebRTCEnabled() {
			hasWebRTC = true
			break
		}
	}
	if !hasWebRTC {
		return nil
	}
	if err := s.webRTC.SetVariants(webrtcVariants); err != nil {
		log.Errorln("webrtc: unable to publish variants; continuing with HLS only:", err)
		return nil
	}

	webrtcConfig := s.configRepository.GetWebRTCConfig()
	videoCodec := s.configRepository.GetVideoCodec()

	outputs := make([]transcoder.RTPOutput, 0, len(webrtcVariants))
	for index := range webrtcVariants {
		variant := webrtcVariants[index]
		if !variant.WebRTCEnabled() {
			continue
		}

		leg, err := s.webRTC.OpenMediaLeg(index)
		if err != nil {
			log.Errorln("webrtc: unable to open media leg for variant", index, ":", err)
			continue
		}
		s.webRTCLegs = append(s.webRTCLegs, index)

		videoBitrate := variant.VideoBitrate
		if videoBitrate <= 0 || variant.IsVideoPassthrough {
			// Copy-eligible variants have no encoder bitrate of their own;
			// WebRTC v1 transcodes them. Runtime copy eligibility is tracked
			// for Phase 4 and reported as a warning today.
			videoBitrate = defaultWebRTCVideoBitrateKbps
		}

		outputs = append(outputs, transcoder.RTPOutput{
			VariantIndex:     index,
			VideoURL:         leg.VideoURL(),
			AudioURL:         leg.AudioURL(),
			VideoPayloadType: 96,
			AudioPayloadType: 111,
			VideoBitrateKbps: videoBitrate,
			GOPFrames:        webrtcGOPFrames(webrtcConfig.KeyframeIntervalSeconds, variant.GetFramerate()),
			H264Profile:      webrtcConfig.H264Profile,
			VideoCodec:       videoCodec,
			CPUUsageLevel:    variant.CPUUsageLevel,
			OpusBitrateKbps:  webrtcConfig.OpusBitrateKbps,
			OpusFrameMs:      webrtcConfig.OpusFrameMs,
			OpusFEC:          webrtcConfig.OpusFEC,
			OpusDTX:          webrtcConfig.OpusDTX,
		})
	}
	return outputs
}

// closeWebRTC tears down all WebRTC media legs for the ended stream.
func (s *Service) closeWebRTC() {
	if s.webRTC == nil {
		return
	}
	for _, index := range s.webRTCLegs {
		_ = s.webRTC.CloseMediaLeg(index)
	}
	s.webRTCLegs = nil
	_ = s.webRTC.SetVariants(nil)
}
