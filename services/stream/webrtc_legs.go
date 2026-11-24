package stream

import (
	"errors"

	log "github.com/sirupsen/logrus"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/ingest"
	"github.com/owncast/owncast/services/transcoder"
	"github.com/owncast/owncast/services/webrtc"
)

// resolveWebRTCSource describes the incoming stream to the WebRTC eligibility
// rules, waiting (bounded) for the source metadata a copy decision needs. The
// inspector may not have published its stream details yet, so an absent
// broadcaster only leaves the codec unknown, which the rules treat as
// "transcode rather than copy". The second result reports that the publisher
// went away during the wait, in which case there is no stream to plan for.
//
// Without WebRTC delivery there is no leg to plan, and the read-ahead that would
// let the inspector publish a verdict is not started either (see
// HandleIngestSession). Waiting for a verdict nothing can produce would only
// delay HLS start by the full source-wait timeout, so this returns at once. The
// gate is the delivery state of the service, not its presence: main.go always
// injects one. Delivery can be enabled between the read-ahead check and this one,
// so the wait additionally requires that this stream was actually read ahead
// (budgetSpent is non-nil); see waitForSourceInfo.
func (s *Service) resolveWebRTCSource(session ingest.Session, webrtcVariants []models.StreamOutputVariant, budgetSpent <-chan struct{}) (webrtc.SourceInfo, bool) {
	if !s.webRTCDeliveryEnabled() {
		return webrtc.SourceInfo{}, false
	}

	broadcaster, cancelled := s.waitForSourceInfo(session, webrtcVariants, budgetSpent)
	if cancelled {
		return webrtc.SourceInfo{}, true
	}
	if broadcaster == nil {
		return webrtc.SourceInfo{Protocol: string(session.Protocol())}, false
	}

	return webrtc.SourceInfoFromDetails(string(session.Protocol()), broadcaster.StreamDetails), false
}

// resolveWebRTCLegs decides how each WebRTC-enabled variant is delivered and
// returns the decisions keyed by variant index, the same decisions rendered for
// the admin status payload, the warnings to report, and whether any variant is
// deliverable at all.
//
// A variant whose leg cannot be delivered is disabled in the published set while
// keeping its slice position, so WHEP variant indices stay stable for the client.
// Its decision is still reported: "this tier was withdrawn" is an answer the
// operator needs, not a silent absence.
//
//nolint:cyclop // linear per-variant plan assembly
func (s *Service) resolveWebRTCLegs(
	webrtcVariants []models.StreamOutputVariant,
	webrtcConfig models.WebRTCConfig,
	source webrtc.SourceInfo,
) (map[int]webrtc.LegDecision, []models.WebRTCLegStatus, []models.StatusWarning, bool) {
	warnings := make([]models.StatusWarning, 0, len(webrtcVariants))
	decisions := make(map[int]webrtc.LegDecision, len(webrtcVariants))
	legStatuses := make([]models.WebRTCLegStatus, 0, len(webrtcVariants))
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

		legStatuses = append(legStatuses, legStatusFromDecision(index, variant, decision))
		logLegDecision(legStatuses[len(legStatuses)-1])

		if !decision.Enabled {
			if webrtcVariants[index].Outputs != nil && webrtcVariants[index].Outputs.WebRTC != nil {
				webrtcVariants[index].Outputs.WebRTC.Enabled = models.BoolPtr(false)
			}

			continue
		}

		decisions[index] = decision
		hasEnabled = true
	}

	return decisions, legStatuses, warnings, hasEnabled
}

// openWebRTCLegs opens one loopback media leg per decided variant and returns the
// RTP outputs the transcoder has to feed. A leg that cannot be opened loses only
// its own variant: the rest, and HLS, keep running.
func (s *Service) openWebRTCLegs(
	webrtcVariants []models.StreamOutputVariant,
	decisions map[int]webrtc.LegDecision,
	webrtcConfig models.WebRTCConfig,
	generation uint64,
	warnings []models.StatusWarning,
) ([]transcoder.RTPOutput, []WebRTCMediaLeg, []models.StatusWarning, []int) {
	videoCodec := s.configRepository.GetVideoCodec()
	outputs := make([]transcoder.RTPOutput, 0, len(decisions))
	// The leg objects travel with the RTP outputs so the supervisor can mark exactly the
	// legs it feeds, without looking them up by index later.
	legs := make([]WebRTCMediaLeg, 0, len(decisions))
	// Variants whose leg could not be opened are withdrawn from WHEP afterwards.
	failed := make([]int, 0)

	for index := range webrtcVariants {
		variant := webrtcVariants[index]
		decision, ok := decisions[index]
		if !ok {
			continue
		}

		leg, err := s.webRTC.OpenMediaLeg(index, generation)
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
			failed = append(failed, index)

			continue
		}

		s.legsMu.Lock()
		s.webRTCLegs = append(s.webRTCLegs, index)
		s.legsMu.Unlock()
		legs = append(legs, leg)

		outputs = append(outputs, transcoder.RTPOutput{
			VariantIndex:     index,
			VideoURL:         leg.VideoURL(),
			AudioURL:         leg.AudioURL(),
			VideoPayloadType: webrtc.PayloadTypeVideo,
			AudioPayloadType: webrtc.PayloadTypeAudio,
			VideoBitrateKbps: decision.VideoBitrateKbps,
			BitratePeakKbps:  decision.BitratePeakKbps,
			// The decision owns the scale: a transcode leg for a variant that
			// does not scale keeps the source resolution ("transcode at source
			// size"), and the isolated WebRTC process decodes the raw source, so
			// it must be told the size explicitly when there is one.
			Scale:           decision.Scale,
			GOPFrames:       decision.GOPFrames,
			Framerate:       decision.Framerate,
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

	return outputs, legs, warnings, failed
}
