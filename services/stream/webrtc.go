package stream

import (
	"fmt"
	"strconv"
	"strings"
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
const webRTCSourceWaitTimeout = 3500 * time.Millisecond

// webRTCReadAheadMaxBytes is the hard memory ceiling for the copy-decision
// read-ahead. The read-ahead itself is bounded by webRTCSourceWaitTimeout; this
// only protects against a source whose bitrate makes that window very large
// (32 MiB covers a 75 Mbps source for the whole window).
const webRTCReadAheadMaxBytes = 32 << 20

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
	// Enabled reports whether the running service delivers WebRTC at all.
	// main.go always injects the service, so a non-nil provider says nothing
	// about delivery; OpenMediaLeg and the WHEP handler both refuse while this
	// is false. Copy planning and the source read-ahead are gated on it too: a
	// verdict nothing can consume would only delay the HLS start.
	Enabled() bool
	// SetVariants publishes the variants WebRTC delivery should expose. The
	// slice position is the variant index used by WHEP and the FFmpeg plan.
	SetVariants(variants []models.StreamOutputVariant) (uint64, error)
	// OpenMediaLeg allocates the Go-owned loopback sockets for a variant and
	// starts the RTP forwarder.
	OpenMediaLeg(variantIndex int, generation uint64) (WebRTCMediaLeg, error)
	// CloseMediaLeg tears the variant leg down.
	CloseMediaLeg(variantIndex int) error
	// VariantGeneration identifies the variant set the caller published.
	VariantGeneration() uint64
	// DisableVariants withdraws variants of the given generation whose media leg is
	// not deliverable, so WHEP cannot hand a viewer a rendition with no media behind
	// it. Cleanup from an older stream is ignored.
	DisableVariants(generation uint64, variantIndices []int)
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

// webRTCDeliveryEnabled reports whether a WebRTC leg can be delivered for the
// stream that is starting. The injected provider is always constructed by
// main.go, so a non-nil provider only means the composition root wired it up;
// delivery is gated by the runtime service's own Enabled(), which is exactly the
// check OpenMediaLeg and the WHEP handler apply. Reading the configuration
// repository instead would add a second source that can disagree with the
// service: the admin update path applies the new configuration to the service
// before it is persisted, and a restart-service change (enabled has
// applyMode=restart-service) keeps the running engine on the previous snapshot
// until the service restarts. The service snapshot is the one that decides
// whether a leg is opened, so it is the one this planning phase follows.
func (s *Service) webRTCDeliveryEnabled() bool {
	return s.webRTC != nil && s.webRTC.Enabled()
}

// webRTCCopyPlanPending reports whether the stream has a WebRTC copy decision
// that source metadata can resolve. It gates the source read-ahead in
// HandleIngestSession and is one half of the wait gate in resolveWebRTCSource:
// with delivery off, or without an SRT source that asks for copy, nothing can
// consume the verdict and HLS must start immediately. The other half is the
// read-ahead itself, because delivery can be switched on between the two reads
// (see waitForSourceInfo).
func (s *Service) webRTCCopyPlanPending(session ingest.Session, variants []models.StreamOutputVariant) bool {
	if !s.webRTCDeliveryEnabled() {
		return false
	}

	return hasWebRTCCopyIntent(variants) && sessionCanReportSourceInfo(session)
}

// sourceInfoReadyWaiter is implemented by ingest sessions that can report the
// moment the source metadata carries a B-frame verdict. RTMP sessions do not
// implement it and never wait.
type sourceInfoReadyWaiter interface {
	SourceInfoReady() <-chan struct{}
}

// waitForSourceInfo blocks briefly for SRT source metadata when a copy decision
// is pending. RTMP metadata cannot provide profile/B-frame information, so RTMP
// never waits (copy intent falls back to transcode by design).
//
// budgetSpent is the read-ahead signal from the prefetch reader, and it is the
// fact that decides whether there is anything to wait for: it is non-nil exactly
// when HandleIngestSession started the read-ahead for this stream, and the wait
// is for the verdict that read-ahead makes the inspector publish. A nil channel
// therefore means no verdict can arrive for this stream, and waiting would only
// burn the full source-wait timeout before HLS starts.
//
// This cannot be derived from webRTCCopyPlanPending alone. HandleIngestSession
// reads that gate to decide whether to read ahead, and the admin applies a
// delivery change immediately, so the switch can be on at this second read while
// the first read saw it off: the stream has no read-ahead, no consumer feeding
// the inspector, and no verdict to wait for. The plan gate below stays as the
// second half of the same decision (an SRT stream that asks for copy); it is not
// a substitute for the read-ahead fact.
func (s *Service) waitForSourceInfo(session ingest.Session, variants []models.StreamOutputVariant, budgetSpent <-chan struct{}) (*models.Broadcaster, bool) {
	if budgetSpent == nil {
		// No read-ahead was started for this stream, so the verdict this wait is
		// for can never be published.
		return session.Broadcaster(), false
	}

	if !s.webRTCCopyPlanPending(session, variants) {
		return session.Broadcaster(), false
	}

	return awaitSourceInfo(session, budgetSpent, webRTCSourceWaitTimeout)
}

// awaitSourceInfo waits for the B-frame verdict a copy decision needs. It returns
// as soon as the verdict is published (the session signals it from the ingest
// read path, so no statistics tick is involved), and it stops waiting early when
// no further evidence can arrive: the read-ahead budget is spent and nothing
// drains it, or the publisher has gone away. Only a source that keeps feeding
// the inspector without deciding runs into the timeout.
//
// The returned broadcaster is whatever is known at that moment. An unknown
// verdict keeps the existing behavior: the eligibility rules treat it as
// "transcode rather than copy". The second result reports that the publisher went
// away while the verdict was pending: that is a cancellation, not an unknown
// source, and the caller must not publish the stream for it.
func awaitSourceInfo(session ingest.Session, budgetSpent <-chan struct{}, timeout time.Duration) (*models.Broadcaster, bool) {
	if broadcaster := session.Broadcaster(); broadcaster != nil && broadcaster.StreamDetails.HasBFrames != nil {
		return broadcaster, false
	}

	var ready <-chan struct{}
	if waiter, ok := session.(sourceInfoReadyWaiter); ok {
		ready = waiter.SourceInfoReady()
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	// The metadata sampler publishes on its own tick, so a source that will
	// never decide (a non-H.264 codec, where the B-frame verdict is irrelevant)
	// is noticed here instead of sitting out the whole timeout.
	poll := time.NewTicker(sourceInfoPollInterval)
	defer poll.Stop()

	for {
		select {
		case <-ready:
			// The verdict is published; the broadcaster carries it now.
			return session.Broadcaster(), false
		case <-budgetSpent:
			// The read-ahead window is over and no consumer drains it yet, so the
			// inspector cannot observe another slice. Stop waiting: the verdict
			// stays unknown and the existing rules fall back to transcode.
			return session.Broadcaster(), false
		case <-session.Done():
			// The publisher went away while the verdict was still pending. This is a
			// cancellation: there is no stream to plan for any more.
			return session.Broadcaster(), true
		case <-poll.C:
			if broadcaster := session.Broadcaster(); sourceDecisionSettled(broadcaster) {
				return broadcaster, false
			}
		case <-timer.C:
			return session.Broadcaster(), false
		}
	}
}

// sourceInfoPollInterval is how often the wait re-reads the published source
// metadata while the B-frame verdict is pending.
const sourceInfoPollInterval = 100 * time.Millisecond

// sourceDecisionSettled reports whether the published source metadata already
// answers the copy question, so waiting longer cannot change the decision. A
// known B-frame verdict settles it; so does a source whose video codec is known
// to be something other than H.264, because such a source must be transcoded
// regardless of its B-frames.
func sourceDecisionSettled(broadcaster *models.Broadcaster) bool {
	if broadcaster == nil {
		return false
	}
	details := broadcaster.StreamDetails
	if details.HasBFrames != nil {
		return true
	}
	codec := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(details.VideoCodec, ".", ""), " ", ""))

	return codec != "" && codec != "h264" && codec != "avc" && codec != "avc1"
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
// appendLegDiagnostics records the warnings and ineligibility reasons a leg
// decision carries, for the admin status panel and the log.
func appendLegDiagnostics(decision webrtc.LegDecision, warnings *[]models.StatusWarning) {
	for _, warning := range decision.Warnings {
		*warnings = append(*warnings, models.StatusWarning{Code: "webrtc_leg", Message: warning})
		log.Warnln("webrtc leg warning:", warning)
	}

	for _, reason := range decision.IneligibilityReasons {
		*warnings = append(*warnings, models.StatusWarning{Code: "webrtc_copy_ineligible", Message: reason})
		log.Infoln("webrtc copy ineligible:", reason)
	}
}

// sessionVideoOnly reports whether the ingest source carries no audio. The
// WebRTC RTP outputs map audio optionally, and an optional map that resolves to
// nothing makes the tee/RTP muxer fail the whole FFmpeg process, so a video-only
// source must not get Opus outputs at all.
func sessionVideoOnly(session ingest.Session) bool {
	broadcaster := session.Broadcaster()
	if broadcaster == nil {
		return false
	}

	return broadcaster.StreamDetails.VideoOnly
}

// sessionCanReportSourceInfo reports whether the session can publish the source
// properties a copy decision needs. Both protocols can now: SRT from the
// MPEG-TS/H.264 inspector and RTMP from the FLV/AVC inspector, which run on the
// same bitstream the transcoder receives.
func sessionCanReportSourceInfo(session ingest.Session) bool {
	_, ok := session.(sourceInfoReadyWaiter)

	return ok
}

// legStatusFromDecision renders one resolved decision for the admin Live
// Stream Status payload. It carries everything needed to answer "why is this
// rendition copied or transcoded": the requested mode, what an inheritance
// resolved against, the mode that was applied, the source properties the rules
// used, the encoder plan, and every failed copy rule.
func legStatusFromDecision(index int, variant models.StreamOutputVariant, decision webrtc.LegDecision) models.WebRTCLegStatus {
	status := models.WebRTCLegStatus{
		VariantIndex:         index,
		Name:                 variant.GetName(),
		RequestedMode:        decision.RequestedMode,
		InheritedFrom:        decision.InheritedFrom,
		Mode:                 decision.Mode,
		IneligibilityReasons: append([]string{}, decision.IneligibilityReasons...),
		Warnings:             append([]string{}, decision.Warnings...),
		Source: models.WebRTCLegSource{
			Protocol:      decision.Source.Protocol,
			VideoCodec:    decision.Source.VideoCodec,
			VideoProfile:  decision.Source.VideoProfile,
			VideoLevel:    decision.Source.VideoLevel,
			HasBFrames:    decision.Source.HasBFrames,
			Width:         decision.Source.Width,
			Height:        decision.Source.Height,
			BitrateSource: decision.Source.BitrateSource,
		},
	}
	if decision.Source.FPS > 0 {
		fps := decision.Source.FPS
		status.Source.FPS = &fps
	}
	if decision.Source.VideoBitrateKbps > 0 {
		bitrate := float64(decision.Source.VideoBitrateKbps)
		status.Source.VideoBitrateKbps = &bitrate
	}
	if decision.Mode == "" {
		status.Mode = models.WebRTCLegModeDisabled
	}
	if decision.Mode == models.OutputModeTranscode {
		status.VideoBitrateKbps = decision.VideoBitrateKbps
		status.BitratePeakKbps = decision.BitratePeakKbps
		status.GOPFrames = decision.GOPFrames
		status.Framerate = decision.Framerate
		status.H264Profile = decision.H264Profile
		if parts := strings.SplitN(decision.Scale, ":", 2); len(parts) == 2 {
			if width, err := strconv.Atoi(parts[0]); err == nil && width > 0 {
				status.ScaledWidth = width
			}
			if height, err := strconv.Atoi(parts[1]); err == nil && height > 0 {
				status.ScaledHeight = height
			}
		}
	}

	return status
}

// webRTCOutputs is what this stream's WebRTC delivery needs: the RTP destinations the
// transcoder feeds and the media legs behind them.
type webRTCOutputs struct {
	outputs []transcoder.RTPOutput
	legs    []WebRTCMediaLeg
}

func (s *Service) configureWebRTC(session ingest.Session, variants []models.StreamOutputVariant, source webrtc.SourceInfo) webRTCOutputs {
	if s.webRTC == nil {
		return webRTCOutputs{}
	}

	webrtcVariants := effectiveWebRTCVariants(variants)
	webrtcConfig := s.configRepository.GetWebRTCConfig()

	decisions, legs, warnings, hasEnabled := s.resolveWebRTCLegs(webrtcVariants, webrtcConfig, source)
	// A transcode leg that keeps the source size and has no explicit bitrate
	// targets the measured source rate. The bitrate window needs about a second
	// of media time, which a copy verdict can beat, so give the measurement one
	// bounded chance and re-plan with it. This happens once, before FFmpeg starts:
	// a bitrate that moves later never restarts the encoder.
	if needsMeasuredBitrate(decisions) {
		if refreshed, ok := s.awaitSourceBitrate(session); ok {
			source = refreshed
			decisions, legs, warnings, hasEnabled = s.resolveWebRTCLegs(webrtcVariants, webrtcConfig, source)
		}
	}
	// The per-leg decisions are recorded before any leg is opened: a tier that
	// cannot be delivered at all is exactly what the operator needs to see.
	s.setWebRTCLegs(legs)
	if !hasEnabled {
		s.setWebRTCWarnings(warnings)
		return webRTCOutputs{}
	}

	generation, err := s.webRTC.SetVariants(webrtcVariants)
	if err != nil {
		log.Errorln("webrtc: unable to publish variants; continuing with HLS only:", err)
		warnings = append(warnings, models.StatusWarning{Code: "webrtc_variants", Message: err.Error()})
		s.setWebRTCWarnings(warnings)
		return webRTCOutputs{}
	}

	s.webRTCMu.Lock()
	s.webRTCVariantGen = generation
	s.webRTCMu.Unlock()

	outputs, mediaLegs, warnings, failed := s.openWebRTCLegs(webrtcVariants, decisions, webrtcConfig, generation, warnings)
	// A variant whose leg failed must not stay selectable over WHEP.
	s.webRTC.DisableVariants(generation, failed)
	s.setWebRTCWarnings(warnings)

	return webRTCOutputs{outputs: outputs, legs: mediaLegs}
}

// webRTCBitrateWaitTimeout bounds the wait for a measured source video bitrate.
// It is only spent when a source-size transcode leg would otherwise have to use
// the documented default.
const webRTCBitrateWaitTimeout = 2500 * time.Millisecond

// needsMeasuredBitrate reports whether any leg is a source-size transcode leg
// that fell back to the default target, i.e. would benefit from waiting for the
// measured source rate.
func needsMeasuredBitrate(decisions map[int]webrtc.LegDecision) bool {
	for _, decision := range decisions {
		if decision.Mode == models.OutputModeTranscode && decision.Scale == "" &&
			decision.BitrateSource == webrtc.BitrateSourceDefault {
			return true
		}
	}

	return false
}

// awaitSourceBitrate waits briefly for the session to publish a measured video
// bitrate and returns the refreshed source description. It reports false when
// the bitrate stays unknown, in which case the caller keeps the documented
// default.
func (s *Service) awaitSourceBitrate(session ingest.Session) (webrtc.SourceInfo, bool) {
	deadline := time.Now().Add(webRTCBitrateWaitTimeout)
	ticker := time.NewTicker(sourceInfoPollInterval)
	defer ticker.Stop()

	for time.Now().Before(deadline) {
		select {
		case <-session.Done():
			return webrtc.SourceInfo{}, false
		case <-ticker.C:
		}

		broadcaster := session.Broadcaster()
		if broadcaster == nil || broadcaster.StreamDetails.VideoBitrate <= 0 {
			continue
		}

		return webrtc.SourceInfoFromDetails(string(session.Protocol()), broadcaster.StreamDetails), true
	}

	return webrtc.SourceInfo{}, false
}

// logLegDecision writes one line per resolved WebRTC leg. The mode, the source
// properties the decision used and the fallback reasons are exactly what the
// admin status payload reports, so a field report can be matched against the
// log without a rebuild.
func logLegDecision(leg models.WebRTCLegStatus) {
	fields := map[string]any{
		"variant":       leg.VariantIndex,
		"name":          leg.Name,
		"requestedMode": leg.RequestedMode,
		"mode":          leg.Mode,
		"protocol":      leg.Source.Protocol,
		"codec":         leg.Source.VideoCodec,
		"profile":       leg.Source.VideoProfile,
		"sourceLevel":   leg.Source.VideoLevel,
		"width":         leg.Source.Width,
		"height":        leg.Source.Height,
	}
	if leg.Source.HasBFrames != nil {
		fields["bFrames"] = *leg.Source.HasBFrames
	} else {
		fields["bFrames"] = "unknown"
	}
	if leg.InheritedFrom != "" {
		fields["inheritedFrom"] = leg.InheritedFrom
	}
	if leg.Mode == models.OutputModeTranscode {
		fields["bitrateKbps"] = leg.VideoBitrateKbps
		fields["gopFrames"] = leg.GOPFrames
		fields["framerate"] = leg.Framerate
		fields["profileOut"] = leg.H264Profile
		if leg.ScaledWidth > 0 && leg.ScaledHeight > 0 {
			fields["scale"] = fmt.Sprintf("%dx%d", leg.ScaledWidth, leg.ScaledHeight)
		}
	}
	if len(leg.IneligibilityReasons) > 0 {
		fields["copyIneligible"] = strings.Join(leg.IneligibilityReasons, "; ")
	}

	log.WithFields(fields).Infof("webrtc: variant %d resolved to %s", leg.VariantIndex, leg.Mode)
}

// setWebRTCLegs records the resolved per-variant decisions for the live status
// payload.
func (s *Service) setWebRTCLegs(legs []models.WebRTCLegStatus) {
	s.webRTCMu.Lock()
	defer s.webRTCMu.Unlock()
	s.webRTCLegsStatus = legs
}

// WebRTCLegs returns the resolved WebRTC delivery decisions for the current
// stream, one per WebRTC-enabled output variant.
func (s *Service) WebRTCLegs() []models.WebRTCLegStatus {
	s.webRTCMu.RLock()
	defer s.webRTCMu.RUnlock()

	return append([]models.WebRTCLegStatus{}, s.webRTCLegsStatus...)
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
	s.legsMu.Lock()
	legs := append([]int(nil), s.webRTCLegs...)
	s.webRTCLegs = nil
	s.legsMu.Unlock()
	for _, index := range legs {
		_ = s.webRTC.CloseMediaLeg(index)
	}
	_, _ = s.webRTC.SetVariants(nil)
	s.setWebRTCWarnings(nil)
	s.setWebRTCLegs(nil)
}
