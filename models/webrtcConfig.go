package models

// WebRTC output/domain binding configuration.
//
// This is the persisted configuration of the WebRTC delivery service. The
// media-plane details (H.264/Opus) are constrained by browsers and are
// documented in docs/design/srt-webrtc-analysis.md (C3).
type WebRTCConfig struct {
	// General.
	Enabled bool `json:"enabled"`

	// Domain binding.
	Domain         string   `json:"domain"`
	PublicIPs      []string `json:"publicIPs"`
	StrictDomain   bool     `json:"strictDomain"`
	AllowedOrigins []string `json:"allowedOrigins"`

	// Limits.
	MaxViewers                int    `json:"maxViewers"`
	MaxViewersPerIP           int    `json:"maxViewersPerIP"`
	SignalingRateLimit        int    `json:"signalingRateLimit"`
	LimitExceededBehavior     string `json:"limitExceededBehavior"`
	SessionIdleTimeoutSeconds int    `json:"sessionIdleTimeoutSeconds"`

	// Network.
	BindAddress    string   `json:"bindAddress"`
	UDPMode        string   `json:"udpMode"`
	UDPPort        int      `json:"udpPort"`
	UDPPortMin     int      `json:"udpPortMin"`
	UDPPortMax     int      `json:"udpPortMax"`
	TCPEnabled     bool     `json:"tcpEnabled"`
	TCPPort        int      `json:"tcpPort"`
	NetworkTypes   []string `json:"networkTypes"`
	InterfaceAllow []string `json:"interfaceAllow"`
	InterfaceDeny  []string `json:"interfaceDeny"`
	MDNS           bool     `json:"mdns"`

	// ICE.
	STUNServers              []string     `json:"stunServers"`
	TURNServers              []TURNServer `json:"turnServers"`
	EmbeddedTurn             bool         `json:"embeddedTurn"`
	EmbeddedTurnPort         int          `json:"embeddedTurnPort"`
	EmbeddedTurnRealm        string       `json:"embeddedTurnRealm"`
	ICELite                  bool         `json:"iceLite"`
	ICEDisconnectedTimeoutMs int          `json:"iceDisconnectedTimeoutMs"`
	ICEFailedTimeoutMs       int          `json:"iceFailedTimeoutMs"`
	ICEKeepaliveIntervalMs   int          `json:"iceKeepaliveIntervalMs"`

	// Media.
	H264Profile             string  `json:"h264Profile"`
	H264Level               string  `json:"h264Level"`
	KeyframeIntervalSeconds float64 `json:"keyframeIntervalSeconds"`
	OpusBitrateKbps         int     `json:"opusBitrateKbps"`
	OpusFrameMs             int     `json:"opusFrameMs"`
	OpusFEC                 bool    `json:"opusFEC"`
	OpusDTX                 bool    `json:"opusDTX"`
	RTPPacketSize           int     `json:"rtpPacketSize"`
	NACK                    bool    `json:"nack"`
	TWCC                    bool    `json:"twcc"`
	BandwidthEstimation     bool    `json:"bandwidthEstimation"`
	MaxBitratePerViewerKbps int     `json:"maxBitratePerViewerKbps"`
	MinBitratePerViewerKbps int     `json:"minBitratePerViewerKbps"`
	RTPReorderBufferMs      int     `json:"rtpReorderBufferMs"`

	// Viewer fallback behavior (client reads this from /api/config).
	FallbackToHLS bool `json:"fallbackToHls"`
}

// TURNServer is one viewer-facing TURN server entry. Credential is a secret
// when it is a long-lived static credential; StaticSecret enables time-limited
// REST-style credentials.
type TURNServer struct {
	URL          string `json:"url"`
	Username     string `json:"username,omitempty"`
	Credential   string `json:"credential,omitempty"`
	StaticSecret string `json:"staticSecret,omitempty"`
}

// DefaultWebRTCConfig returns the WebRTC defaults; disabled so the default
// install is byte-identical in behavior to v0.3.1.
func DefaultWebRTCConfig() WebRTCConfig {
	return WebRTCConfig{
		Enabled:                   false,
		Domain:                    "",
		PublicIPs:                 []string{},
		StrictDomain:              false,
		AllowedOrigins:            []string{},
		MaxViewers:                100,
		MaxViewersPerIP:           0,
		SignalingRateLimit:        30,
		LimitExceededBehavior:     "reject",
		SessionIdleTimeoutSeconds: 30,
		BindAddress:               "0.0.0.0",
		UDPMode:                   "mux",
		UDPPort:                   8189,
		UDPPortMin:                50000,
		UDPPortMax:                50100,
		TCPEnabled:                true,
		TCPPort:                   8189,
		NetworkTypes:              []string{"udp4", "udp6", "tcp4", "tcp6"},
		InterfaceAllow:            []string{},
		InterfaceDeny:             []string{},
		MDNS:                      false,
		STUNServers:               []string{},
		TURNServers:               []TURNServer{},
		EmbeddedTurn:              false,
		EmbeddedTurnPort:          3478,
		EmbeddedTurnRealm:         "owncast",
		ICELite:                   false,
		ICEDisconnectedTimeoutMs:  5000,
		ICEFailedTimeoutMs:        25000,
		ICEKeepaliveIntervalMs:    2000,
		H264Profile:               "constrained-baseline",
		H264Level:                 "auto",
		KeyframeIntervalSeconds:   1,
		OpusBitrateKbps:           96,
		OpusFrameMs:               20,
		OpusFEC:                   true,
		OpusDTX:                   false,
		RTPPacketSize:             1200,
		NACK:                      true,
		TWCC:                      true,
		BandwidthEstimation:       false,
		MaxBitratePerViewerKbps:   0,
		MinBitratePerViewerKbps:   0,
		RTPReorderBufferMs:        0,
		FallbackToHLS:             true,
	}
}

// OutputProcessModel values for output.processModel.
const (
	OutputProcessModelShared   = "shared"
	OutputProcessModelIsolated = "isolated"
)
