package webrtc

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/owncast/owncast/models"
)

// Copy-eligibility reason codes. The prefixed strings are stable so the admin
// UI and tests can switch on them; each reason is formatted as "code: detail".
const (
	ReasonNoSourceInfo       = "webrtc_copy_ineligible_no_source_info"
	ReasonCodecUnsupported   = "webrtc_copy_ineligible_codec"
	ReasonProfileUnknown     = "webrtc_copy_ineligible_profile_unknown"
	ReasonProfileUnsupported = "webrtc_copy_ineligible_profile"
	ReasonBFramesPresent     = "webrtc_copy_ineligible_b_frames"
	ReasonBFramesUnknown     = "webrtc_copy_ineligible_b_frames_unknown"
	ReasonResolutionUnknown  = "webrtc_copy_ineligible_resolution_unknown"
	ReasonResolutionTooLarge = "webrtc_copy_ineligible_resolution_too_large"
	ReasonResolutionOdd      = "webrtc_copy_ineligible_resolution_odd"
	ReasonLevelUnknown       = "webrtc_copy_ineligible_level_unknown"
	ReasonLevelUnsupported   = "webrtc_copy_ineligible_level"
)

// Copy-decision warning codes appended to LegDecision.Warnings.
const (
	WarningCopyFallbackTranscode = "webrtc_copy_fallback_transcode"
	WarningCopyDisabled          = "webrtc_copy_disabled"
	WarningCopyProfileMismatch   = "webrtc_copy_profile_mismatch"
	WarningCopySourceGOP         = "webrtc_copy_source_gop"
	WarningInheritNoHLS          = "webrtc_inherit_no_hls"
	WarningCopyFallbackInvalid   = "webrtc_copy_fallback_invalid"
)

// H.264 profile labels as reported by the SRT MPEG-TS inspector and used by
// the settings metadata; they are shared with the encoder argv mapping.
const (
	h264ProfileConstrainedBaseline = "constrained-baseline"
	h264ProfileBaseline            = "baseline"
	h264ProfileMain                = "main"
	h264ProfileHigh                = "high"
	h264ProfileConstrainedHigh     = "constrained-high"
)

// videoCodecH264 is the normalized source codec label for H.264.
const videoCodecH264 = "h264"

const (
	// browserSafeMaxLevel is the highest H.264 level (encoded as major*10+minor,
	// e.g. 51 == 5.1) accepted for stream copy. Levels above it exceed what
	// browsers reliably decode from a copied stream.
	browserSafeMaxLevel = 51
	// maxCopyWidth/maxCopyHeight cap copied video resolution at 4K UHD, which
	// browsers decode reliably at H.264 level 5.1. The previous 1920x1080 cap
	// unnecessarily forced common 1440p sources through a transcode; 4K is the
	// practical browser limit and larger sources must still be transcoded.
	maxCopyWidth  = 4096
	maxCopyHeight = 2160

	// DefaultWebRTCVideoBitrateKbps is the transcode fallback bitrate when a
	// copy variant (which carries no encoder bitrate) has to be transcoded.
	DefaultWebRTCVideoBitrateKbps = 2500
	// DefaultWebRTCFrameRate is used when the source frame rate is unknown.
	DefaultWebRTCFrameRate = 30
	// MinimumWebRTCGOPFrames keeps the encoder GOP at least one frame.
	MinimumWebRTCGOPFrames = 1
	// defaultKeyframeIntervalSeconds matches the persisted WebRTC default.
	defaultKeyframeIntervalSeconds = 1.0
)

// SourceInfo is the source-stream property set available when a stream starts,
// derived from models.InboundStreamDetails (SRT: MPEG-TS/H.264 inspector;
// RTMP: onMetaData). Zero values mean "unknown"; the copy decision is
// conservative when a property is unknown.
type SourceInfo struct {
	// Protocol is "rtmp", "srt" or "" when the caller does not know it.
	Protocol string `json:"protocol,omitempty"`
	// VideoCodec is normalized to lowercase ("h264", "hevc", "av1", "", ...).
	VideoCodec string `json:"videoCodec,omitempty"`
	// VideoProfile is the inspector profile label ("constrained-baseline",
	// "baseline", "main", "high", "constrained-high", ...), "" when unknown.
	VideoProfile string `json:"videoProfile,omitempty"`
	// VideoLevel is the H.264 level in "major.minor" form ("4.0", "5.1", ...),
	// "" when unknown.
	VideoLevel string `json:"videoLevel,omitempty"`
	// HasBFrames is nil when the protocol cannot report it (RTMP) or the
	// inspector has not decided yet.
	HasBFrames *bool   `json:"hasBFrames,omitempty"`
	Width      int     `json:"width,omitempty"`
	Height     int     `json:"height,omitempty"`
	FPS        float64 `json:"fps,omitempty"`
}

// LegDecision is the resolved WebRTC delivery decision for one configured
// output variant. It is a pure value: no I/O, no state.
type LegDecision struct {
	// Enabled is false when the variant is not delivered over WebRTC at all
	// (WebRTC disabled for the variant, or copyFallback=disable).
	Enabled bool `json:"enabled"`
	// Mode is the effective mode: models.OutputModeCopy or
	// models.OutputModeTranscode. Empty when !Enabled.
	Mode string `json:"mode,omitempty"`
	// Copy is a convenience flag for Mode == copy.
	Copy bool `json:"copy"`
	// VideoBitrateKbps/GOPFrames are the effective transcode settings (zero for
	// copy legs, which carry no encoder parameters).
	VideoBitrateKbps int `json:"videoBitrateKbps,omitempty"`
	GOPFrames        int `json:"gopFrames,omitempty"`
	// H264Profile is the configured profile advertised by transcode legs.
	H264Profile string `json:"h264Profile,omitempty"`
	// Source echoes the input metadata used for the decision (for the admin UI).
	Source SourceInfo `json:"source"`
	// IneligibilityReasons lists why a requested copy leg could not be copied.
	IneligibilityReasons []string `json:"ineligibilityReasons,omitempty"`
	// Warnings are operator-facing warnings (fallback applied, disabled, ...).
	Warnings []string `json:"warnings,omitempty"`
}

// SourceInfoFromDetails converts models.InboundStreamDetails into the pure
// eligibility input. protocol is the ingest protocol ("rtmp" or "srt").
func SourceInfoFromDetails(protocol string, details models.InboundStreamDetails) SourceInfo {
	return SourceInfo{
		Protocol:     strings.ToLower(strings.TrimSpace(protocol)),
		VideoCodec:   normalizeCodecName(details.VideoCodec),
		VideoProfile: normalizeProfileName(details.VideoProfile),
		VideoLevel:   strings.TrimSpace(details.VideoLevel),
		HasBFrames:   details.HasBFrames,
		Width:        details.Width,
		Height:       details.Height,
		FPS:          float64(details.VideoFramerate),
	}
}

// SourceInfoFromBroadcaster converts the broadcaster metadata published by the
// ingest protocols. The protocol itself is not part of models.Broadcaster, so
// callers that know it should set SourceInfo.Protocol afterwards.
func SourceInfoFromBroadcaster(broadcaster *models.Broadcaster) SourceInfo {
	if broadcaster == nil {
		return SourceInfo{}
	}

	return SourceInfoFromDetails("", broadcaster.StreamDetails)
}

func normalizeCodecName(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	normalized = strings.ReplaceAll(normalized, ".", "")
	normalized = strings.ReplaceAll(normalized, " ", "")

	switch normalized {
	case videoCodecH264, "avc", "avc1":
		return videoCodecH264
	case "h265", "hevc", "hvc1":
		return "hevc"
	default:
		return normalized
	}
}

func normalizeProfileName(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	normalized = strings.ReplaceAll(normalized, "_", "-")

	return normalized
}

// EligibleForCopy implements the spec C4 copy-eligibility rules for an H.264
// source. It returns false plus one prefixed reason per failed rule.
//
// Rules (documented for the operator guide):
//  1. The source video codec must be H.264; anything else (HEVC/AV1/unknown)
//     must be transcoded to H.264 (spec C3).
//  2. The H.264 profile must be a browser-safe profile: constrained-baseline,
//     baseline, main, high or constrained-high. High 10/4:2:2/4:4:4, extended
//     and unknown profiles are rejected.
//  3. B-frames must be known and absent. Unknown B-frame state (RTMP
//     onMetaData) is not eligible: reordering breaks the browser jitter buffer.
//  4. Resolution must be known, even in both dimensions and at most 4096x2160
//     (4K UHD; the largest size browsers decode reliably at H.264 level 5.1).
//  5. The H.264 level must be known and at most 5.1 (or the explicitly
//     configured target level when that is lower).
//
// cfg supplies the configured H.264 profile/level: an explicit target level
// lowers the copy level cap; the configured profile is only used for the
// advertised-SDP warning in ResolveWebRTCLeg, not to reject browser-safe copy.
func EligibleForCopy(source SourceInfo, cfg models.WebRTCConfig) (bool, []string) {
	if sourceIsEmpty(source) {
		return false, []string{reasonf(ReasonNoSourceInfo, "source metadata is not available yet")}
	}

	maxLevel := browserSafeMaxLevel
	if configured, ok := parseH264Level(cfg.H264Level); ok && configured < maxLevel && configured > 0 {
		maxLevel = configured
	}

	reasons := make([]string, 0, 5)
	reasons = append(reasons, codecCopyReasons(source.VideoCodec)...)
	reasons = append(reasons, profileCopyReasons(source.VideoProfile)...)
	reasons = append(reasons, bFrameCopyReasons(source.HasBFrames)...)
	reasons = append(reasons, resolutionCopyReasons(source.Width, source.Height)...)
	reasons = append(reasons, levelCopyReasons(source.VideoLevel, maxLevel)...)

	return len(reasons) == 0, reasons
}

func sourceIsEmpty(source SourceInfo) bool {
	return source.VideoCodec == "" && source.VideoProfile == "" && source.Width == 0 && source.Height == 0
}

func codecCopyReasons(codec string) []string {
	if codec == videoCodecH264 {
		return nil
	}
	if codec == "" {
		return []string{reasonf(ReasonCodecUnsupported, "source video codec is unknown")}
	}

	return []string{reasonf(ReasonCodecUnsupported, "source video codec %s is not H.264", codec)}
}

func profileCopyReasons(profile string) []string {
	switch profile {
	case h264ProfileConstrainedBaseline, h264ProfileBaseline, h264ProfileMain,
		h264ProfileHigh, h264ProfileConstrainedHigh:
		return nil
	case "":
		return []string{reasonf(ReasonProfileUnknown, "source H.264 profile is unknown")}
	default:
		return []string{reasonf(ReasonProfileUnsupported, "source H.264 profile %q is not browser-safe", profile)}
	}
}

func bFrameCopyReasons(hasBFrames *bool) []string {
	if hasBFrames == nil {
		return []string{reasonf(ReasonBFramesUnknown, "B-frame usage is unknown for this protocol")}
	}
	if *hasBFrames {
		return []string{reasonf(ReasonBFramesPresent, "source uses B-frames")}
	}

	return nil
}

func resolutionCopyReasons(width, height int) []string {
	if width <= 0 || height <= 0 {
		return []string{reasonf(ReasonResolutionUnknown, "source resolution is unknown")}
	}
	if width > maxCopyWidth || height > maxCopyHeight {
		return []string{reasonf(ReasonResolutionTooLarge,
			"source resolution %dx%d exceeds %dx%d", width, height, maxCopyWidth, maxCopyHeight)}
	}
	if width%2 != 0 || height%2 != 0 {
		return []string{reasonf(ReasonResolutionOdd, "source resolution %dx%d has odd dimensions", width, height)}
	}

	return nil
}

func levelCopyReasons(level string, maxLevel int) []string {
	if strings.TrimSpace(level) == "" {
		return []string{reasonf(ReasonLevelUnknown, "source H.264 level is unknown")}
	}
	parsed, ok := parseH264Level(level)
	if !ok {
		return []string{reasonf(ReasonLevelUnsupported, "source H.264 level %q is invalid", level)}
	}
	if parsed > maxLevel {
		return []string{reasonf(ReasonLevelUnsupported,
			"source H.264 level %s exceeds the browser-safe cap %s",
			level, formatH264Level(maxLevel))}
	}

	return nil
}

// parseH264Level parses "4.0"/"5.1" (or "40"/"51") into major*10+minor.
func parseH264Level(level string) (int, bool) {
	level = strings.TrimSpace(level)
	if level == "" || strings.EqualFold(level, "auto") {
		return 0, false
	}
	parts := strings.SplitN(level, ".", 2)
	major, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || major < 0 {
		return 0, false
	}

	if len(parts) == 1 {
		// Accept the compact forms "4" (level 4.0) and "51" (level 5.1) in
		// addition to the canonical "major.minor" form.
		digits := strings.TrimSpace(parts[0])
		switch len(digits) {
		case 1:
			return major * 10, true
		case 2:
			return major, true
		default:
			return 0, false
		}
	}

	parsedMinor, minorErr := strconv.Atoi(strings.TrimSpace(parts[1]))
	if minorErr != nil || parsedMinor < 0 || parsedMinor > 9 {
		return 0, false
	}

	return major*10 + parsedMinor, true
}

func formatH264Level(level int) string {
	return fmt.Sprintf("%d.%d", level/10, level%10)
}

func reasonf(code, format string, args ...any) string {
	return code + ": " + fmt.Sprintf(format, args...)
}

// ResolveWebRTCLeg applies the variant's WebRTC mode (inherit/transcode/copy)
// and, for copy legs, the runtime eligibility decision and copyFallback policy.
// It is pure: the caller (stream lifecycle) supplies the source metadata and
// receives the plan it should hand to the transcoder.
func ResolveWebRTCLeg(source SourceInfo, variant models.StreamOutputVariant, cfg models.WebRTCConfig) (LegDecision, error) {
	decision := LegDecision{Source: source}
	if !variant.WebRTCEnabled() {
		return decision, nil
	}

	decision.Enabled = true
	configuredMode, err := configuredWebRTCMode(variant)
	if err != nil {
		return LegDecision{}, err
	}

	effectiveMode, modeWarnings := effectiveWebRTCMode(variant, configuredMode)
	decision.Warnings = append(decision.Warnings, modeWarnings...)

	if effectiveMode == models.OutputModeTranscode {
		return finalizeTranscodeDecision(decision, variant, cfg), nil
	}

	explicitCopy := configuredMode == models.OutputModeCopy

	eligible, reasons := EligibleForCopy(source, cfg)
	if eligible {
		return finalizeCopyDecision(decision, cfg, explicitCopy), nil
	}

	return applyCopyFallback(decision, variant, cfg, reasons, explicitCopy), nil
}

func configuredWebRTCMode(variant models.StreamOutputVariant) (string, error) {
	output := variant.WebRTCOutput()
	if output == nil {
		return models.OutputModeInherit, nil
	}

	mode := strings.ToLower(strings.TrimSpace(output.Mode))
	switch mode {
	case "", models.OutputModeInherit:
		return models.OutputModeInherit, nil
	case models.OutputModeTranscode, models.OutputModeCopy:
		return mode, nil
	default:
		return "", fmt.Errorf("%w: unknown webrtc output mode %q for variant %q",
			ErrInvalidConfig, output.Mode, variant.GetName())
	}
}

// effectiveWebRTCMode resolves "inherit" against the HLS variant: an HLS copy
// (video passthrough) variant inherits copy, everything else inherits
// transcode (spec section 4.4 mapping rule / T7).
func effectiveWebRTCMode(variant models.StreamOutputVariant, configuredMode string) (string, []string) {
	if configuredMode != models.OutputModeInherit {
		return configuredMode, nil
	}
	if !variant.HLSEnabled() {
		return models.OutputModeTranscode, []string{
			WarningInheritNoHLS + ": HLS is disabled for this variant, so the inherited WebRTC mode is transcode",
		}
	}
	if variantUsesVideoCopy(variant) {
		return models.OutputModeCopy, nil
	}

	return models.OutputModeTranscode, nil
}

func variantUsesVideoCopy(variant models.StreamOutputVariant) bool {
	if variant.IsVideoPassthrough {
		return true
	}
	if hls := variant.HLSOutput(); hls != nil && strings.EqualFold(strings.TrimSpace(hls.Mode), models.OutputModeCopy) {
		return true
	}

	return false
}

func finalizeTranscodeDecision(decision LegDecision, variant models.StreamOutputVariant, cfg models.WebRTCConfig) LegDecision {
	bitrate, gop := effectiveTranscodeSettings(variant, cfg)
	decision.Mode = models.OutputModeTranscode
	decision.Copy = false
	decision.VideoBitrateKbps = bitrate
	decision.GOPFrames = gop
	decision.H264Profile = effectiveH264Profile(cfg)

	return decision
}

func finalizeCopyDecision(decision LegDecision, cfg models.WebRTCConfig, explicitCopy bool) LegDecision {
	decision.Mode = models.OutputModeCopy
	decision.Copy = true
	decision.IneligibilityReasons = nil
	decision.H264Profile = effectiveH264Profile(cfg)
	if !explicitCopy {
		// Copy inherited from an HLS copy variant is our automatic policy, not
		// an operator choice; keep the admin status quiet for it.
		return decision
	}
	decision.Warnings = append(decision.Warnings,
		WarningCopySourceGOP+": copy legs keep the source GOP, so join latency is bounded by the encoder's keyframe interval")

	if sourceProfile := decision.Source.VideoProfile; sourceProfile != "" &&
		!profileFamiliesCompatible(sourceProfile, cfg.H264Profile) {
		decision.Warnings = append(decision.Warnings, reasonf(WarningCopyProfileMismatch,
			"source profile %q differs from the configured %q; the answer advertises the configured profile while the copied bitstream keeps its own SPS",
			sourceProfile, effectiveH264Profile(cfg)))
	}

	return decision
}

// applyCopyFallback applies the configured fallback when a copy leg cannot be
// used. explicitCopy is true only when the operator explicitly asked for copy
// delivery: an inherited copy (an HLS passthrough variant whose WebRTC mode is
// "inherit") falling back to transcode is normal and must not surface warnings
// in the admin UI - only an explicit copy request that cannot be honored, or a
// leg that gets disabled, is operator-actionable.
func applyCopyFallback(decision LegDecision, variant models.StreamOutputVariant,
	cfg models.WebRTCConfig, reasons []string, explicitCopy bool,
) LegDecision {
	if explicitCopy {
		decision.IneligibilityReasons = append([]string(nil), reasons...)
	}

	fallback, fallbackWarnings := copyFallbackMode(variant)
	decision.Warnings = append(decision.Warnings, fallbackWarnings...)
	if fallback == models.CopyFallbackDisable {
		decision.Enabled = false
		decision.Mode = ""
		decision.Copy = false
		decision.VideoBitrateKbps = 0
		decision.GOPFrames = 0
		message := "WebRTC delivery disabled for this variant because the source is not copy-eligible"
		if len(reasons) > 0 {
			message = fmt.Sprintf("%s (%s)", message, strings.Join(reasons, "; "))
		}
		decision.Warnings = append(decision.Warnings, reasonf(WarningCopyDisabled, "%s", message))

		return decision
	}

	decision = finalizeTranscodeDecision(decision, variant, cfg)
	if !explicitCopy {
		// Inherited copy -> transcode is the expected path for ordinary
		// transcode variants; do not report it as a source problem.
		decision.IneligibilityReasons = nil

		return decision
	}
	decision.Warnings = append(decision.Warnings, reasonf(WarningCopyFallbackTranscode,
		"copy was requested but the source is not copy-eligible, so it is transcoded to H.264 instead (%s)",
		strings.Join(reasons, "; ")))

	return decision
}

func copyFallbackMode(variant models.StreamOutputVariant) (string, []string) {
	output := variant.WebRTCOutput()
	if output == nil {
		return models.CopyFallbackTranscode, nil
	}

	fallback := strings.ToLower(strings.TrimSpace(output.CopyFallback))
	switch fallback {
	case "":
		return models.CopyFallbackTranscode, nil
	case models.CopyFallbackTranscode, models.CopyFallbackDisable:
		return fallback, nil
	default:
		return models.CopyFallbackTranscode, []string{
			reasonf(WarningCopyFallbackInvalid, "unknown copyFallback %q, using %s",
				output.CopyFallback, models.CopyFallbackTranscode),
		}
	}
}

func effectiveTranscodeSettings(variant models.StreamOutputVariant, cfg models.WebRTCConfig) (bitrateKbps, gopFrames int) {
	override := variant.WebRTCOutput()

	bitrateKbps = variant.VideoBitrate
	if override != nil && override.VideoBitrate > 0 {
		bitrateKbps = override.VideoBitrate
	}
	if bitrateKbps <= 0 {
		bitrateKbps = DefaultWebRTCVideoBitrateKbps
	}

	fps := variant.GetFramerate()
	if override != nil && override.Framerate > 0 {
		fps = override.Framerate
	}
	if fps <= 0 {
		fps = DefaultWebRTCFrameRate
	}

	interval := cfg.KeyframeIntervalSeconds
	if interval <= 0 {
		interval = defaultKeyframeIntervalSeconds
	}
	gopFrames = int(math.Round(interval * float64(fps)))
	if gopFrames < MinimumWebRTCGOPFrames {
		gopFrames = MinimumWebRTCGOPFrames
	}

	return bitrateKbps, gopFrames
}

func effectiveH264Profile(cfg models.WebRTCConfig) string {
	if profile := strings.TrimSpace(cfg.H264Profile); profile != "" {
		return profile
	}

	return "constrained-baseline"
}

// profileFamiliesCompatible reports whether a source profile can be delivered
// under the configured profile's SDP fmtp without surprising strict decoders.
// Baseline sources are valid under main/high advertisements; higher sources are
// not downgraded silently in the SDP.
func profileFamiliesCompatible(sourceProfile, configuredProfile string) bool {
	sourceRank := profileRank(normalizeProfileName(sourceProfile))
	configuredRank := profileRank(normalizeProfileName(configuredProfile))
	if sourceRank < 0 || configuredRank < 0 {
		return false
	}

	return sourceRank <= configuredRank
}

func profileRank(profile string) int {
	switch profile {
	case h264ProfileConstrainedBaseline, h264ProfileBaseline:
		return 0
	case h264ProfileMain:
		return 1
	case h264ProfileHigh, h264ProfileConstrainedHigh:
		return 2
	default:
		return -1
	}
}
