package settings

import "github.com/owncast/owncast/models"

// WithSRTDefaults fills fields whose stored zero value cannot be a valid
// operator choice with the documented default. It is used when a configuration
// is read back from the KV store so that a config written by an older build (or
// a partially populated value) cannot end up with, for example, port 0.
//
// Explicitly legal zero values are preserved: authFailureRateLimit (0 disables
// the limiter), debugStatsIntervalMs (0 disables debug logging), ipTTL/ipTOS
// (0 means "leave the OS default"), enabled=false, enforcedEncryption=false and
// driftTracer=false are never overwritten.
func WithSRTDefaults(config models.SRTConfig) models.SRTConfig {
	defaults := models.DefaultSRTConfig()

	if config.BindAddress == "" {
		config.BindAddress = defaults.BindAddress
	}
	if config.Port < 1024 {
		config.Port = defaults.Port
	}
	if config.StreamIDFormats == nil {
		config.StreamIDFormats = defaults.StreamIDFormats
	}
	if config.AllowedCIDRs == nil {
		config.AllowedCIDRs = []string{}
	}
	if config.DeniedCIDRs == nil {
		config.DeniedCIDRs = []string{}
	}
	if config.ReceiverLatencyMs == 0 {
		config.ReceiverLatencyMs = defaults.ReceiverLatencyMs
	}
	if config.PeerLatencyMs == 0 {
		config.PeerLatencyMs = defaults.PeerLatencyMs
	}
	if config.FlowControlWindowPackets == 0 {
		config.FlowControlWindowPackets = defaults.FlowControlWindowPackets
	}
	if config.MSS == 0 {
		config.MSS = defaults.MSS
	}
	if config.PayloadSize == 0 {
		config.PayloadSize = defaults.PayloadSize
	}
	if config.PeerIdleTimeoutMs == 0 {
		config.PeerIdleTimeoutMs = defaults.PeerIdleTimeoutMs
	}
	if config.LogTopics == nil {
		config.LogTopics = []string{}
	}
	if config.MaxPendingHandshakes == 0 {
		config.MaxPendingHandshakes = defaults.MaxPendingHandshakes
	}

	return config
}

// WithWebRTCDefaults fills fields whose stored zero value cannot be a valid
// operator choice with the documented default. Explicitly legal zero values
// are preserved: maxViewers, maxViewersPerIP, maxBitratePerViewerKbps,
// minBitratePerViewerKbps, rtpReorderBufferMs (all "off"/unlimited) and every
// boolean except where the zero value would contradict another field.
func WithWebRTCDefaults(config models.WebRTCConfig) models.WebRTCConfig {
	normalizeWebRTCListDefaults(&config)
	normalizeWebRTCGeneralDefaults(&config)
	normalizeWebRTCNetworkDefaults(&config)
	normalizeWebRTCICEDefaults(&config)
	normalizeWebRTCMediaDefaults(&config)
	return config
}

// normalizeWebRTCListDefaults turns nil lists into empty lists so JSON output
// stays stable.
func normalizeWebRTCListDefaults(config *models.WebRTCConfig) {
	if config.PublicIPs == nil {
		config.PublicIPs = []string{}
	}
	if config.AllowedOrigins == nil {
		config.AllowedOrigins = []string{}
	}
	if config.InterfaceAllow == nil {
		config.InterfaceAllow = []string{}
	}
	if config.InterfaceDeny == nil {
		config.InterfaceDeny = []string{}
	}
	if config.STUNServers == nil {
		config.STUNServers = []string{}
	}
	if config.TURNServers == nil {
		config.TURNServers = []models.TURNServer{}
	}
}

// normalizeWebRTCGeneralDefaults fills the general and limits defaults.
func normalizeWebRTCGeneralDefaults(config *models.WebRTCConfig) {
	defaults := models.DefaultWebRTCConfig()
	if config.LimitExceededBehavior == "" {
		config.LimitExceededBehavior = defaults.LimitExceededBehavior
	}
	if config.SessionIdleTimeoutSeconds == 0 {
		config.SessionIdleTimeoutSeconds = defaults.SessionIdleTimeoutSeconds
	}
	if config.SignalingRateLimit == 0 {
		config.SignalingRateLimit = defaults.SignalingRateLimit
	}
}

// normalizeWebRTCNetworkDefaults fills the listener and port defaults.
func normalizeWebRTCNetworkDefaults(config *models.WebRTCConfig) {
	defaults := models.DefaultWebRTCConfig()
	if config.BindAddress == "" {
		config.BindAddress = defaults.BindAddress
	}
	if config.UDPMode == "" {
		config.UDPMode = defaults.UDPMode
	}
	if config.UDPPort < 1024 {
		config.UDPPort = defaults.UDPPort
	}
	if config.UDPPortMin < 1024 {
		config.UDPPortMin = defaults.UDPPortMin
	}
	if config.UDPPortMax < 1024 {
		config.UDPPortMax = defaults.UDPPortMax
	}
	if config.TCPPort < 1024 {
		config.TCPPort = defaults.TCPPort
	}
	// len() rather than == nil: a persisted empty list is just as unset. An empty
	// list left the ICE network types with no entries at all, so no candidate of any
	// type could be gathered.
	if len(config.NetworkTypes) == 0 {
		if config.TCPEnabled {
			config.NetworkTypes = defaultNetworkTypes()
		} else {
			config.NetworkTypes = []string{networkTypeUDP4, networkTypeUDP6}
		}
	}
}

// normalizeWebRTCICEDefaults fills the ICE and TURN defaults.
func normalizeWebRTCICEDefaults(config *models.WebRTCConfig) {
	defaults := models.DefaultWebRTCConfig()
	if config.EmbeddedTurnPort < 1024 {
		config.EmbeddedTurnPort = defaults.EmbeddedTurnPort
	}
	if config.EmbeddedTurnRealm == "" {
		config.EmbeddedTurnRealm = defaults.EmbeddedTurnRealm
	}
	if config.ICEDisconnectedTimeoutMs == 0 {
		config.ICEDisconnectedTimeoutMs = defaults.ICEDisconnectedTimeoutMs
	}
	if config.ICEFailedTimeoutMs == 0 {
		config.ICEFailedTimeoutMs = defaults.ICEFailedTimeoutMs
	}
	if config.ICEKeepaliveIntervalMs == 0 {
		config.ICEKeepaliveIntervalMs = defaults.ICEKeepaliveIntervalMs
	}
}

// normalizeWebRTCMediaDefaults fills the media/codec defaults.
func normalizeWebRTCMediaDefaults(config *models.WebRTCConfig) {
	defaults := models.DefaultWebRTCConfig()
	if config.H264Profile == "" {
		config.H264Profile = defaults.H264Profile
	}
	if config.H264Level == "" {
		config.H264Level = defaults.H264Level
	}
	if config.KeyframeIntervalSeconds == 0 {
		config.KeyframeIntervalSeconds = defaults.KeyframeIntervalSeconds
	}
	if config.OpusBitrateKbps == 0 {
		config.OpusBitrateKbps = defaults.OpusBitrateKbps
	}
	if config.OpusFrameMs == 0 {
		config.OpusFrameMs = defaults.OpusFrameMs
	}
	if config.RTPPacketSize < 500 {
		config.RTPPacketSize = defaults.RTPPacketSize
	}
}
