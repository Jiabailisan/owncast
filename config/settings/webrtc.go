package settings

import "github.com/owncast/owncast/models"

// WebRTCMetadata returns the metadata registry for every persisted
// models.WebRTCConfig field, followed by the browser-fixed entries.
func WebRTCMetadata() []Field {
	return append(webrtcPersistedMetadata(), webrtcFixedMetadata()...)
}

// Network type values are the ICE network types accepted by the metadata enum
// and stored in models.WebRTCConfig.NetworkTypes.
const (
	networkTypeUDP4 = "udp4"
	networkTypeUDP6 = "udp6"
	networkTypeTCP4 = "tcp4"
	networkTypeTCP6 = "tcp6"
)

// defaultNetworkTypes is the default ICE network type set.
func defaultNetworkTypes() []string {
	return []string{networkTypeUDP4, networkTypeUDP6, networkTypeTCP4, networkTypeTCP6}
}

// webrtcPersistedMetadata is the persisted section of the WebRTC registry.
func webrtcPersistedMetadata() []Field {
	fields := []Field{
		boolField("enabled", groupWebRTCGeneral, models.DefaultWebRTCConfig().Enabled,
			"Enable WebRTC low-latency delivery and the WHEP signaling routes. Disabled by default.", ApplyRestartService),
		boolField("fallbackToHls", groupWebRTCGeneral, true,
			"Client behavior: fall back to HLS when WebRTC capacity, ICE or playback fails.", ApplyLive),

		stringField("domain", groupWebRTCDomain, "", "FQDN",
			"Public domain used for ICE candidate advertisement and the WHEP URL. Empty uses the configured server URL host.", ApplyRestartService),
		stringListField("publicIPs", groupWebRTCDomain, []string{},
			"Public IP addresses advertised as ICE candidates. Overrides DNS resolution of the domain.", ApplyRestartService),
		boolField("strictDomain", groupWebRTCDomain, false,
			"Reject WHEP signaling when the request Host or Origin does not match the configured domain.", ApplyLive),
		stringListField("allowedOrigins", groupWebRTCDomain, []string{},
			"Allowed browser origins for signaling when strictDomain is enabled, e.g. https://example.com.", ApplyLive),

		intField("maxViewers", groupWebRTCLimits, 100, 0, 100000, "viewers",
			"Maximum concurrent WebRTC viewers. 0 is unlimited.", ApplyLive),
		intField("maxViewersPerIP", groupWebRTCLimits, 0, 0, 1000, "viewers",
			"Maximum concurrent WebRTC viewers per IP address. 0 disables the per-IP cap.", ApplyLive),
		intField("signalingRateLimit", groupWebRTCLimits, 30, 1, 10000, "requests/min/IP",
			"WHEP signaling request budget per IP and minute.", ApplyLive),
		enumField("limitExceededBehavior", groupWebRTCLimits, "reject", []string{"reject"},
			"Behavior when the viewer limit is reached: reject with HTTP 503 and Retry-After (the viewer falls back to HLS client-side).", ApplyLive),
		intField("sessionIdleTimeoutSeconds", groupWebRTCLimits, 30, 5, 600, "seconds",
			"Close a WebRTC session when no activity is observed for this long.", ApplyLive),

		stringField("bindAddress", groupWebRTCNetwork, "0.0.0.0", "address",
			"IP address for WebRTC UDP/TCP listeners. Must be an IP literal.", ApplyRestartService),
		enumField("udpMode", groupWebRTCNetwork, "mux", []string{"mux", "range"},
			"UDP port strategy: a single mux port or an ephemeral port range.", ApplyRestartService),
		intField("udpPort", groupWebRTCNetwork, 8189, 1024, 65535, "port",
			"UDP port for mux mode. Must not collide with the SRT, web or RTMP ports.", ApplyRestartService),
		intField("udpPortMin", groupWebRTCNetwork, 50000, 1024, 65535, "port",
			"First port of the UDP range used in range mode.", ApplyRestartService),
		intField("udpPortMax", groupWebRTCNetwork, 50100, 1024, 65535, "port",
			"Last port of the UDP range used in range mode. Must be greater than udpPortMin.", ApplyRestartService),
		boolField("tcpEnabled", groupWebRTCNetwork, true,
			"Enable ICE-TCP candidates for viewers behind UDP-blocking networks.", ApplyRestartService),
		intField("tcpPort", groupWebRTCNetwork, 8189, 1024, 65535, "port",
			"TCP port for the ICE-TCP mux. May equal udpPort (different transport) but not the web or RTMP port.", ApplyRestartService),
		{
			Key:         "networkTypes",
			Label:       fieldLabel("networkTypes"),
			Group:       groupWebRTCNetwork,
			Type:        TypeStringList,
			Default:     defaultNetworkTypes(),
			Enum:        defaultNetworkTypes(),
			Description: "ICE network types to gather. TCP entries require tcpEnabled.",
			ApplyMode:   ApplyRestartService,
		},
		stringListField("interfaceAllow", groupWebRTCNetwork, []string{},
			"Network interfaces to include for ICE candidates. Empty allows all.", ApplyRestartService),
		stringListField("interfaceDeny", groupWebRTCNetwork, []string{},
			"Network interfaces to exclude from ICE candidates. Deny wins over allow.", ApplyRestartService),
		boolField("mdns", groupWebRTCNetwork, false,
			"Enable mDNS ICE candidate obfuscation for server candidates. Not useful for a public server.", ApplyRestartService),

		stringListField("stunServers", groupWebRTCICE, []string{},
			"STUN URLs used by the server for candidate discovery, e.g. stun:stun.example.org:3478. TURN URLs are not accepted here.", ApplyRestartService),
		turnServerListField(),
		// Embedded TURN is not implemented in this release. The field stays declared so
		// the persisted model round-trips, and validation rejects enabling it with an
		// error attached to this field instead of a generic one.
		boolField("embeddedTurn", groupWebRTCICE, false,
			"Run the optional embedded TURN server for viewers. Not available in this release; configure an external TURN server instead.", ApplyRestartService),
		intField("embeddedTurnPort", groupWebRTCICE, 3478, 1024, 65535, "port",
			"UDP port for the embedded TURN server. Reserved while embedded TURN is unavailable.", ApplyRestartService),
		stringField("embeddedTurnRealm", groupWebRTCICE, "owncast", "realm",
			"TURN authentication realm used by the embedded TURN server. Reserved while embedded TURN is unavailable.", ApplyRestartService),
		boolField("iceLite", groupWebRTCICE, false,
			"Enable ICE lite mode. Incompatible with TURN relaying in most topologies.", ApplyRestartService),
		intField("iceDisconnectedTimeoutMs", groupWebRTCICE, 5000, 1000, 120000, "ms",
			"ICE disconnected timeout. Must be lower than iceFailedTimeoutMs and higher than iceKeepaliveIntervalMs.", ApplyRestartService),
		intField("iceFailedTimeoutMs", groupWebRTCICE, 25000, 2000, 300000, "ms",
			"ICE failed timeout. Must be higher than iceDisconnectedTimeoutMs.", ApplyRestartService),
		intField("iceKeepaliveIntervalMs", groupWebRTCICE, 2000, 500, 60000, "ms",
			"ICE keepalive interval. Must be lower than iceDisconnectedTimeoutMs.", ApplyRestartService),

		enumField("h264Profile", groupWebRTCMedia, "constrained-baseline",
			[]string{"constrained-baseline", "baseline", "main", "high"},
			"H.264 profile for WebRTC video legs. Browsers reliably decode Constrained Baseline, Main and High.", ApplyRestartStream),
		enumField("h264Level", groupWebRTCMedia, "auto",
			[]string{"auto", "3.0", "3.1", "4.0", "4.1", "4.2", "5.0", "5.1", "5.2", "6.0", "6.1", "6.2"},
			"H.264 level for WebRTC video legs. auto lets the encoder choose. Levels above 4.2 cover 1440p/4K WebRTC output.", ApplyRestartStream),
		floatField("keyframeIntervalSeconds", groupWebRTCMedia, 1, 0.5, 10, "seconds",
			"Fixed GOP length for WebRTC legs. Bounds viewer join time and keyframe availability.", ApplyRestartStream),
		intField("opusBitrateKbps", groupWebRTCMedia, 96, 16, 256, "kbps",
			"Opus audio bitrate for WebRTC legs. Audio is always transcoded to Opus.", ApplyRestartStream),
		{
			Key:         "opusFrameMs",
			Label:       fieldLabel("opusFrameMs"),
			Group:       groupWebRTCMedia,
			Type:        TypeInt,
			Default:     20,
			Enum:        []string{"10", "20", "40", "60"},
			Description: "Opus frame duration in milliseconds. 20 is recommended for low latency and loss resilience.",
			ApplyMode:   ApplyRestartStream,
		},
		intField("rtpPacketSize", groupWebRTCMedia, 1200, 500, 1400, "bytes",
			"RTP packet payload size for WebRTC legs. 1200 stays below typical path MTUs.", ApplyRestartStream),
		boolField("nack", groupWebRTCMedia, true,
			"Answer viewer NACK feedback with RTP retransmission from a bounded packet cache.", ApplyRestartService),
		boolField("twcc", groupWebRTCMedia, true,
			"Enable transport-wide congestion control feedback.", ApplyRestartService),
		boolField("bandwidthEstimation", groupWebRTCMedia, false,
			"Run the receive-side bandwidth estimator for monitoring. Requires twcc. No encoder adaptation is performed.", ApplyRestartService),
		intField("maxBitratePerViewerKbps", groupWebRTCMedia, 0, 0, 100000, "kbps",
			"Reserved and not enforced: every viewer shares one leg per rendition, so there is no per-viewer rate to cap.", ApplyLive),
		intField("minBitratePerViewerKbps", groupWebRTCMedia, 0, 0, 100000, "kbps",
			"Reserved and not enforced: every viewer shares one leg per rendition, so there is no per-viewer rate floor.", ApplyLive),
		intField("rtpReorderBufferMs", groupWebRTCMedia, 0, 0, 2000, "ms",
			"Forwarder RTP reorder buffer. 0 disables reordering.", ApplyRestartService),
	}
	return fields
}

// turnServerListField builds the structured viewer-facing TURN server list.
// This type is an extension of the contract type list (see FieldType).
func turnServerListField() Field {
	return Field{
		Key:         "turnServers",
		Label:       fieldLabel("turnServers"),
		Group:       groupWebRTCICE,
		Type:        TypeTurnServerList,
		Default:     []models.TURNServer{},
		Description: "Viewer-facing TURN servers. Each entry is either {url, username, credential} or {url, staticSecret} for time-limited REST credentials. credential and staticSecret are write-only secrets and are never returned.",
		Secret:      true,
		ApplyMode:   ApplyLive,
	}
}

// webrtcFixedMetadata documents browser/protocol constraints that are not
// settings. See the package documentation for why these are metadata-only.
func webrtcFixedMetadata() []Field {
	return []Field{
		readOnlyField("videoCodec", groupWebRTCFixed, TypeEnum, "h264", "",
			"Browsers reliably decode H.264 only; HEVC/AV1 sources must be transcoded."),
		readOnlyField("audioCodec", groupWebRTCFixed, TypeEnum, "opus", "",
			"WebRTC audio is always Opus. AAC can never be passed through, so audio is always transcoded."),
		readOnlyField("iceTransport", groupWebRTCFixed, TypeEnum, "dtls-srtp", "",
			"DTLS-SRTP is mandatory for WebRTC; plaintext RTP is never exposed to viewers."),
		readOnlyField("opusFEC", groupWebRTCFixed, TypeBool, true, "",
			"Fixed: Opus in-band FEC cannot be selected through FFmpeg's RTP muxer (-fec is an FEC protocol selector, not a boolean), so no argv can enable it. The value is documented but never applied."),
		readOnlyField("opusDTX", groupWebRTCFixed, TypeBool, false, "",
			"Fixed: Opus DTX has no FFmpeg argv representation and is therefore never applied."),
	}
}
