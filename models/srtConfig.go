package models

// SRTStreamIDFormat names an accepted SRT streamid grammar for the ingest
// listener. The zero value of a config means "all formats".
type SRTStreamIDFormat string

const (
	// SRTStreamIDRawKey is a bare stream key ("abc123").
	SRTStreamIDRawKey SRTStreamIDFormat = "raw-key"
	// SRTStreamIDPublishPath is Owncast's RTMP-like path form
	// ("publish:/live/abc123").
	SRTStreamIDPublishPath SRTStreamIDFormat = "publish:/live/<key>"
	// SRTStreamIDAccessControl is the SRT access-control grammar
	// ("#!::r=live/abc123,m=publish").
	SRTStreamIDAccessControl SRTStreamIDFormat = "#!::r=<app>/<key>,m=publish"
)

// AllSRTStreamIDFormats is the default accepted set.
func AllSRTStreamIDFormats() []string {
	return []string{
		string(SRTStreamIDRawKey),
		string(SRTStreamIDPublishPath),
		string(SRTStreamIDAccessControl),
	}
}

// SRTConfig is the persisted configuration of the SRT ingest listener.
//
// Field semantics and the "not supported" surface are documented in
// docs/design/phase0/02-srt-dependency-and-design.md. Only settings that have
// a proven runtime effect in the pinned gosrt version are stored here; the
// operator-facing settings reference is generated from the metadata registry
// in config/settings.
type SRTConfig struct {
	// Listener group.
	Enabled     bool   `json:"enabled"`
	BindAddress string `json:"bindAddress"`
	Port        int    `json:"port"`

	// Auth group.
	StreamIDFormats      []string `json:"streamIdFormats"`
	RequireStreamID      bool     `json:"requireStreamId"`
	AllowedCIDRs         []string `json:"allowedCIDRs"`
	DeniedCIDRs          []string `json:"deniedCIDRs"`
	AuthFailureRateLimit int      `json:"authFailureRateLimit"` // failures per minute per IP, 0 disables

	// Latency and reliability group.
	ReceiverLatencyMs int `json:"receiverLatencyMs"`
	PeerLatencyMs     int `json:"peerLatencyMs"`

	// Buffers and packets group.
	FlowControlWindowPackets int `json:"flowControlWindowPackets"`
	MSS                      int `json:"mss"`
	PayloadSize              int `json:"payloadSize"`

	// Encryption group. Passphrase is a secret: the admin API returns
	// {set: bool} instead of the value and logs must never contain it.
	Passphrase         string `json:"passphrase"`
	EnforcedEncryption bool   `json:"enforcedEncryption"`

	// Timeouts group.
	PeerIdleTimeoutMs int `json:"peerIdleTimeoutMs"`

	// Network QoS group (IPv4 only; 0 leaves the OS default).
	IPTTL int `json:"ipTTL"`
	IPTOS int `json:"ipTOS"`

	// Diagnostics group.
	LogTopics            []string `json:"logTopics"`
	DebugStatsIntervalMs int      `json:"debugStatsIntervalMs"`

	// Advanced group.
	DriftTracer          bool `json:"driftTracer"`
	MaxPendingHandshakes int  `json:"maxPendingHandshakes"`
}

// DefaultSRTConfig returns the SRT defaults. Disabled by default so an
// upgraded server keeps the v0.3.1 behavior until the operator opts in.
func DefaultSRTConfig() SRTConfig {
	return SRTConfig{
		Enabled:                  false,
		BindAddress:              "0.0.0.0",
		Port:                     8890,
		StreamIDFormats:          AllSRTStreamIDFormats(),
		RequireStreamID:          true,
		AllowedCIDRs:             []string{},
		DeniedCIDRs:              []string{},
		AuthFailureRateLimit:     5,
		ReceiverLatencyMs:        120,
		PeerLatencyMs:            120,
		FlowControlWindowPackets: 25600,
		MSS:                      1500,
		PayloadSize:              1316,
		Passphrase:               "",
		EnforcedEncryption:       true,
		PeerIdleTimeoutMs:        5000,
		IPTTL:                    0,
		IPTOS:                    0,
		LogTopics:                []string{},
		DebugStatsIntervalMs:     0,
		DriftTracer:              true,
		MaxPendingHandshakes:     32,
	}
}

// SRTStreamIDFormatsContains reports whether the configured list accepts the
// given format.
func SRTStreamIDFormatsContains(formats []string, format SRTStreamIDFormat) bool {
	if len(formats) == 0 {
		return true
	}
	for _, f := range formats {
		if f == string(format) {
			return true
		}
	}
	return false
}
