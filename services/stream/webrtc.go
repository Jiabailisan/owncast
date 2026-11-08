package stream

import (
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/ingest"
	"github.com/owncast/owncast/services/transcoder"
	"github.com/owncast/owncast/services/webrtc"
)

// webRTCSourceWaitTimeout bounds how long the stream waits for SRT source
// metadata before planning WebRTC legs. The MPEG-TS inspector usually publishes
// within about a second; without metadata any copy intent falls back to
// transcode, which is the safe default.
const webRTCSourceWaitTimeout = 2500 * time.Millisecond

// WebRTCMediaLeg is the loopback RTP leg contract exposed by the WebRTC
// delivery service. One leg carries one video and one audio RTP stream.
type WebRTCMediaLeg interface {
	VideoURL() string
	AudioURL() string
	Close() error
	// MarkRestart tells the forwarder a new RTP producer starts: sequence
	// numbers/timestamps are rebased (isolated process model restarts).
	MarkRestart()
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

// hasWebRTCCopyIntent reports whether any WebRTC-enabled variant asks for copy
// delivery (explicitly or by inheriting a passthrough HLS variant). Copy
// eligibility needs source metadata, so only these streams wait for it.
func hasWebRTCCopyIntent(variants []models.StreamOutputVariant) bool {
	for _, variant := range variants {
		if !variant.WebRTCEnabled() {
			continue
		}
		mode := ""
		if variant.Outputs != nil && variant.Outputs.WebRTC != nil {
			mode = variant.Outputs.WebRTC.Mode
		}
		if mode == models.OutputModeCopy {
			return true
		}
		if (mode == "" || mode == models.OutputModeInherit) && variant.IsVideoPassthrough {
			return true
		}
	}
	return false
}

// waitForSourceInfo blocks briefly for SRT source metadata when a copy decision
// is pending. RTMP metadata cannot provide profile/B-frame information, so RTMP
// never waits (copy intent falls back to transcode by design).
func (s *Service) waitForSourceInfo(session ingest.Session, variants []models.StreamOutputVariant) *models.Broadcaster {
	if session.Protocol() != ingest.ProtocolSRT || !hasWebRTCCopyIntent(variants) {
		return session.Broadcaster()
	}
	// A copy decision needs the B-frame verdict, not just the first metadata
	// tick. Wait (bounded) until the inspector has observed enough slices to
	// decide, otherwise fall back to transcode.
	deadline := time.Now().Add(webRTCSourceWaitTimeout)
	for {
		broadcaster := session.Broadcaster()
		if broadcaster != nil && broadcaster.StreamDetails.HasBFrames != nil {
			return broadcaster
		}
		if time.Now().After(deadline) {
			return broadcaster
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// configureWebRTC resolves the WebRTC plan for the stream that is starting and
// returns the RTP outputs the transcoder must produce. Copy-eligible legs are
// decided from the source metadata (spec C4/T7); anything unknown or ineligible
// falls back to transcode (or is disabled when copyFallback=disable). Failure is
// non-fatal: HLS continues and the condition is surfaced as a live-status
// warning.
// The function is a linear plan assembly: one branch per ineligible/disabled
// leg plus error handling for each step.
//
//nolint:cyclop // linear per-variant plan assembly
func (s *Service) configureWebRTC(session ingest.Session, variants []models.StreamOutputVariant) []transcoder.RTPOutput {
	if s.webRTC == nil {
		return nil
	}

	webrtcVariants := effectiveWebRTCVariants(variants)
	webrtcConfig := s.configRepository.GetWebRTCConfig()

	broadcaster := s.waitForSourceInfo(session, webrtcVariants)
	source := webrtc.SourceInfo{Protocol: string(session.Protocol())}
	if broadcaster != nil {
		source = webrtc.SourceInfoFromDetails(string(session.Protocol()), broadcaster.StreamDetails)
	}

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
		for _, warning := range decision.Warnings {
			warnings = append(warnings, models.StatusWarning{Code: "webrtc_leg", Message: warning})
			log.Warnln("webrtc leg warning:", warning)
		}
		for _, reason := range decision.IneligibilityReasons {
			warnings = append(warnings, models.StatusWarning{Code: "webrtc_copy_ineligible", Message: reason})
			log.Infoln("webrtc copy ineligible:", reason)
		}

		if !decision.Enabled {
			// Disable the leg in the published variant set but keep the slice
			// position so WHEP variant indices stay stable.
			if webrtcVariants[index].Outputs != nil && webrtcVariants[index].Outputs.WebRTC != nil {
				webrtcVariants[index].Outputs.WebRTC.Enabled = models.BoolPtr(false)
			}
			continue
		}
		decisions[index] = decision
		hasEnabled = true
	}
	if !hasEnabled {
		s.setWebRTCWarnings(warnings)
		return nil
	}

	if err := s.webRTC.SetVariants(webrtcVariants); err != nil {
		log.Errorln("webrtc: unable to publish variants; continuing with HLS only:", err)
		warnings = append(warnings, models.StatusWarning{Code: "webrtc_variants", Message: err.Error()})
		s.setWebRTCWarnings(warnings)
		return nil
	}

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
			log.Errorln("webrtc: unable to open media leg for variant", index, ":", err)
			warnings = append(warnings, models.StatusWarning{Code: "webrtc_media_leg", Message: err.Error()})
			continue
		}
		s.webRTCLegs = append(s.webRTCLegs, index)
		s.registerWebRTCLeg(index, leg)

		outputs = append(outputs, transcoder.RTPOutput{
			VariantIndex:     index,
			VideoURL:         leg.VideoURL(),
			AudioURL:         leg.AudioURL(),
			VideoPayloadType: 96,
			AudioPayloadType: 111,
			VideoBitrateKbps: decision.VideoBitrateKbps,
			GOPFrames:        decision.GOPFrames,
			CopyVideo:        decision.Copy,
			H264Profile:      decision.H264Profile,
			VideoCodec:       videoCodec,
			CPUUsageLevel:    variant.CPUUsageLevel,
			OpusBitrateKbps:  webrtcConfig.OpusBitrateKbps,
			OpusFrameMs:      webrtcConfig.OpusFrameMs,
			OpusFEC:          webrtcConfig.OpusFEC,
			OpusDTX:          webrtcConfig.OpusDTX,
		})
	}
	s.setWebRTCWarnings(warnings)
	return outputs
}

// setWebRTCWarnings records the current stream's WebRTC warnings for the live
// status payload.
func (s *Service) setWebRTCWarnings(warnings []models.StatusWarning) {
	s.webRTCMu.Lock()
	defer s.webRTCMu.Unlock()
	s.webRTCWarnings = warnings
}

// WebRTCWarnings returns the warnings produced by WebRTC leg planning for the
// current stream.
func (s *Service) WebRTCWarnings() []models.StatusWarning {
	s.webRTCMu.RLock()
	defer s.webRTCMu.RUnlock()
	return append([]models.StatusWarning(nil), s.webRTCWarnings...)
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
	s.clearWebRTCLegs()
	_ = s.webRTC.SetVariants(nil)
	s.setWebRTCWarnings(nil)
}
