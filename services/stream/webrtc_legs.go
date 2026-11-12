package stream

import (
	"errors"

	log "github.com/sirupsen/logrus"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/ingest"
	"github.com/owncast/owncast/services/transcoder"
	"github.com/owncast/owncast/services/webrtc"
)

// webrtcSourceInfo describes the incoming stream to the WebRTC eligibility rules.
// The inspector may not have published its stream details yet, so an absent
// broadcaster only leaves the codec unknown, which the rules treat as
// "transcode rather than copy".
func (s *Service) webrtcSourceInfo(session ingest.Session, webrtcVariants []models.StreamOutputVariant) webrtc.SourceInfo {
	broadcaster := s.waitForSourceInfo(session, webrtcVariants)
	if broadcaster == nil {
		return webrtc.SourceInfo{Protocol: string(session.Protocol())}
	}

	return webrtc.SourceInfoFromDetails(string(session.Protocol()), broadcaster.StreamDetails)
}

// resolveWebRTCLegs decides how each WebRTC-enabled variant is delivered and
// returns the decisions keyed by variant index, the warnings to report, and
// whether any variant is deliverable at all.
//
// A variant whose leg cannot be delivered is disabled in the published set while
// keeping its slice position, so WHEP variant indices stay stable for the client.
func (s *Service) resolveWebRTCLegs(
	session ingest.Session,
	webrtcVariants []models.StreamOutputVariant,
	webrtcConfig models.WebRTCConfig,
	source webrtc.SourceInfo,
) (map[int]webrtc.LegDecision, []models.StatusWarning, bool) {
	warnings := make([]models.StatusWarning, 0, len(webrtcVariants))
	decisions := make(map[int]webrtc.LegDecision, len(webrtcVariants))
	hasEnabled := false

	for index := range webrtcVariants {
		variant := webrtcVariants[index]
		if !variant.WebRTCEnabled() {
			continue
		}

		decision, err := webrtc.ResolveWebRTCLeg(source, variant, webrtcConfig)
		if err != nil {
			log.Errorln("webrtc: invalid output variant; falling back to transcode:", err)

			decision = webrtc.LegDecision{
				Enabled:          true,
				Mode:             models.OutputModeTranscode,
				VideoBitrateKbps: webrtc.DefaultWebRTCVideoBitrateKbps,
				GOPFrames:        webrtc.MinimumWebRTCGOPFrames,
				H264Profile:      webrtcConfig.H264Profile,
			}
		}

		appendLegDiagnostics(decision, &warnings)
		// RTMP cannot provide the strict copy-eligibility metadata. When the
		// operator asked for passthrough (explicit copy or inheritance from a
		// passthrough HLS variant), deliver the copy leg with an honest warning.
		// The forwarder supplies cached SPS/PPS per IDR, so a viewer joining
		// mid-stream can still decode it.
		decision = rtmpCopyFallback(session, variant, decision, source, webrtcConfig, &warnings, index)

		if !decision.Enabled {
			if webrtcVariants[index].Outputs != nil && webrtcVariants[index].Outputs.WebRTC != nil {
				webrtcVariants[index].Outputs.WebRTC.Enabled = models.BoolPtr(false)
			}

			continue
		}

		decisions[index] = decision
		hasEnabled = true
	}

	return decisions, warnings, hasEnabled
}

// openWebRTCLegs opens one loopback media leg per decided variant and returns the
// RTP outputs the transcoder has to feed. A leg that cannot be opened loses only
// its own variant: the rest, and HLS, keep running.
func (s *Service) openWebRTCLegs(
	webrtcVariants []models.StreamOutputVariant,
	decisions map[int]webrtc.LegDecision,
	webrtcConfig models.WebRTCConfig,
	warnings []models.StatusWarning,
) ([]transcoder.RTPOutput, []models.StatusWarning) {
	videoCodec := s.configRepository.GetVideoCodec()
	outputs := make([]transcoder.RTPOutput, 0, len(decisions))

	for index := range webrtcVariants {
		variant := webrtcVariants[index]
		decision, ok := decisions[index]
		if !ok {
			continue
		}

		leg, err := s.webRTC.OpenMediaLeg(index)
		if err != nil {
			// Delivery being switched off is a configuration, not a failure: logging
			// it as an error made every stream start look broken on installs that do
			// not use WebRTC.
			if errors.Is(err, webrtc.ErrDisabled) {
				log.Debugln("webrtc: delivery is disabled; skipping media leg for variant", index)
			} else {
				log.Errorln("webrtc: unable to open media leg for variant", index, ":", err)
			}

			warnings = append(warnings, models.StatusWarning{Code: "webrtc_media_leg", Message: err.Error()})

			continue
		}

		s.legsMu.Lock()
		s.webRTCLegs = append(s.webRTCLegs, index)
		s.legsMu.Unlock()
		s.registerWebRTCLeg(index, leg)

		outputs = append(outputs, transcoder.RTPOutput{
			VariantIndex:     index,
			VideoURL:         leg.VideoURL(),
			AudioURL:         leg.AudioURL(),
			VideoPayloadType: webrtc.PayloadTypeVideo,
			AudioPayloadType: webrtc.PayloadTypeAudio,
			VideoBitrateKbps: decision.VideoBitrateKbps,
			// The isolated WebRTC process decodes the raw source, so a transcode
			// leg must be told the variant's size explicitly or it keeps the
			// source resolution (a 1440p source stayed 1440p at 6 Mbps).
			Scale:           webrtcLegScale(variant, decision),
			GOPFrames:       decision.GOPFrames,
			CopyVideo:       decision.Copy,
			H264Profile:     decision.H264Profile,
			VideoCodec:      videoCodec,
			CPUUsageLevel:   variant.CPUUsageLevel,
			OpusBitrateKbps: webrtcConfig.OpusBitrateKbps,
			OpusFrameMs:     webrtcConfig.OpusFrameMs,
			OpusFEC:         webrtcConfig.OpusFEC,
			OpusDTX:         webrtcConfig.OpusDTX,
		})
	}

	return outputs, warnings
}
