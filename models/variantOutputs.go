package models

// VariantOutputs extends a configured output variant with per-protocol delivery
// settings. It is additive: a nil Outputs (and a nil HLS block) means the
// legacy HLS-only behavior, so existing stored configurations keep working
// unchanged. WebRTC delivery for a variant is disabled unless explicitly
// enabled.
type VariantOutputs struct {
	HLS    *ProtocolOutputSettings `json:"hls,omitempty"`
	WebRTC *ProtocolOutputSettings `json:"webrtc,omitempty"`
}

// ProtocolOutputSettings overrides one output variant for one delivery
// protocol. Zero values inherit the variant/global settings.
type ProtocolOutputSettings struct {
	// Enabled defaults to true for HLS (legacy behavior) and false for WebRTC.
	Enabled *bool `json:"enabled,omitempty"`

	// Mode is "inherit", "transcode" or "copy" for WebRTC, and
	// "transcode"/"copy" for HLS. HLS mode copy is expressed by the legacy
	// videoPassthrough flags.
	Mode string `json:"mode,omitempty"`

	// CopyFallback is "transcode" (default) or "disable": what to do when a
	// copy-eligible variant's source turns out to be ineligible at runtime.
	CopyFallback string `json:"copyFallback,omitempty"`

	// Per-protocol overrides. Zero means inherit.
	VideoBitrate  int  `json:"videoBitrate,omitempty"`
	AudioBitrate  int  `json:"audioBitrate,omitempty"`
	Framerate     int  `json:"framerate,omitempty"`
	CPUUsageLevel *int `json:"cpuUsageLevel,omitempty"`
	ScaledWidth   int  `json:"scaledWidth,omitempty"`
	ScaledHeight  int  `json:"scaledHeight,omitempty"`
}

// Protocol output modes.
const (
	OutputModeInherit   = "inherit"
	OutputModeTranscode = "transcode"
	OutputModeCopy      = "copy"
)

// Copy fallbacks.
const (
	CopyFallbackTranscode = "transcode"
	CopyFallbackDisable   = "disable"
)

// BoolPtr is a small helper for pointer-typed JSON settings.
func BoolPtr(v bool) *bool { return &v }

// HLSEnabled reports whether this variant is delivered over HLS. Legacy
// variants with no outputs block are HLS-only.
func (q *StreamOutputVariant) HLSEnabled() bool {
	if q.Outputs == nil || q.Outputs.HLS == nil || q.Outputs.HLS.Enabled == nil {
		return true
	}
	return *q.Outputs.HLS.Enabled
}

// WebRTCEnabled reports whether this variant is delivered over WebRTC.
func (q *StreamOutputVariant) WebRTCEnabled() bool {
	if q.Outputs == nil || q.Outputs.WebRTC == nil || q.Outputs.WebRTC.Enabled == nil {
		return false
	}
	return *q.Outputs.WebRTC.Enabled
}

// WebRTCOutput returns the WebRTC settings for this variant, if enabled.
func (q *StreamOutputVariant) WebRTCOutput() *ProtocolOutputSettings {
	if !q.WebRTCEnabled() {
		return nil
	}
	return q.Outputs.WebRTC
}

// HLSOutput returns the HLS settings for this variant, if enabled.
func (q *StreamOutputVariant) HLSOutput() *ProtocolOutputSettings {
	if !q.HLSEnabled() {
		return nil
	}
	if q.Outputs == nil {
		return nil
	}
	return q.Outputs.HLS
}
