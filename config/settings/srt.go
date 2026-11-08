package settings

import "github.com/owncast/owncast/models"

// SRTMetadata returns the metadata registry for every persisted
// models.SRTConfig field, in form order, followed by the read-only entries
// that document SRT options fixed by the library or the live-media protocol.
//
// The persisted entries are derived one-to-one from the model's json tags; the
// reflection test in metadata_test.go fails if a model field is missing here
// or if a read-only entry ever collides with a persisted key.
func SRTMetadata() []Field {
	return append(srtPersistedMetadata(), srtFixedMetadata()...)
}

// srtPersistedMetadata is the persisted section of the SRT registry.
func srtPersistedMetadata() []Field {
	streamIDFormats := models.AllSRTStreamIDFormats()
	fields := []Field{
		boolField("enabled", groupSRTListener, models.DefaultSRTConfig().Enabled,
			"Start the in-process SRT ingest listener. UDP is a new public surface; enable it deliberately.", ApplyRestartService),
		stringField("bindAddress", groupSRTListener, "0.0.0.0", "address",
			"IP address the SRT listener binds to. Must be an IP literal, not a hostname.", ApplyRestartService),
		intField("port", groupSRTListener, 8890, 1024, 65535, "port",
			"UDP port the SRT listener binds. Must not collide with the web, RTMP or WebRTC ports.", ApplyRestartService),

		{
			Key:         "streamIdFormats",
			Label:       fieldLabel("streamIdFormats"),
			Group:       groupSRTAuth,
			Type:        TypeStringList,
			Default:     streamIDFormats,
			Enum:        streamIDFormats,
			Description: "Accepted SRT streamid grammars. At least one format is required when requireStreamId is enabled.",
			ApplyMode:   ApplyLive,
		},
		boolField("requireStreamId", groupSRTAuth, true,
			"Require a streamid carrying the stream key. SRT handshake v4 publishers have no streamid and are rejected.", ApplyLive),
		cidrListField("allowedCIDRs", groupSRTAuth, []string{},
			"CIDR prefixes allowed to connect. Empty allows every address. Deny entries win over allow entries.", ApplyLive),
		cidrListField("deniedCIDRs", groupSRTAuth, []string{},
			"CIDR prefixes refused before authentication. Evaluated before the allow list.", ApplyLive),
		intField("authFailureRateLimit", groupSRTAuth, 5, 0, 1000, "failures/min/IP",
			"Per-IP authentication failure budget per minute. 0 disables the limiter.", ApplyLive),

		intField("receiverLatencyMs", groupSRTLatency, 120, 20, 10000, "ms",
			"SRT TSBPD receiver latency. Higher values tolerate more loss and jitter, at the cost of latency.", ApplyRestartService),
		intField("peerLatencyMs", groupSRTLatency, 120, 20, 10000, "ms",
			"TSBPD latency advertised to the publisher. Should be >= the publisher's latency setting.", ApplyRestartService),

		intField("flowControlWindowPackets", groupSRTBuffers, 25600, 32, 1000000, "packets",
			"SRT flow control window (FC). Larger values tolerate more in-flight data on high-latency links.", ApplyRestartService),
		intField("mss", groupSRTBuffers, 1500, 76, 1500, "bytes",
			"Maximum segment size for SRT packets. payloadSize must be <= mss - 44.", ApplyRestartService),
		intField("payloadSize", groupSRTBuffers, 1316, 188, 1456, "bytes",
			"SRT payload size. 1316 fits seven 188-byte MPEG-TS packets; must be <= mss - 44.", ApplyRestartService),

		secretField("passphrase", groupSRTEncryption,
			"Optional SRT link-encryption passphrase (10 to 64 characters). Write-only: the API returns passphraseSet, never the value, and it is never logged.", ApplyRestartService),
		boolField("enforcedEncryption", groupSRTEncryption, true,
			"Reject unencrypted publishers when a passphrase is configured. Implemented by the SRT accept pipeline, not by the library.", ApplyLive),

		intField("peerIdleTimeoutMs", groupSRTTimeouts, 5000, 500, 60000, "ms",
			"Close a publisher connection that receives no packets for this long. Also the stale-connection reaper.", ApplyRestartService),

		intField("ipTTL", groupSRTNetwork, 0, 0, 255, "hops",
			"IP TTL for SRT packets. 0 leaves the OS default. Only valid with an IPv4 bind address.", ApplyRestartService),
		intField("ipTOS", groupSRTNetwork, 0, 0, 255, "TOS",
			"IP TOS/DSCP byte for SRT packets. 0 leaves the OS default. Only valid with an IPv4 bind address.", ApplyRestartService),

		stringListField("logTopics", groupSRTDiagnostics, []string{},
			"gosrt internal log topics to enable. Unknown topics are accepted but produce no output.", ApplyLive),
		intField("debugStatsIntervalMs", groupSRTDiagnostics, 0, 0, 60000, "ms",
			"Emit an SRT stats debug log line at this interval. 0 disables debug logging; the live status sampler always runs.", ApplyLive),

		boolField("driftTracer", groupSRTAdvanced, true,
			"Correct TSBPD time-base drift between the encoder and the server clock.", ApplyRestartService),
		intField("maxPendingHandshakes", groupSRTAdvanced, 32, 1, 1024, "connections",
			"Maximum concurrent unauthenticated SRT handshakes. Excess requests are rejected with resource errors.", ApplyLive),
	}
	return fields
}

// srtFixedMetadata documents the gosrt v0.13.0 options that are fixed for an
// Owncast live-ingest listener. They are metadata-only: they are not persisted
// and cannot be changed. Presenting them read-only satisfies spec C2 (expose
// the intersection with srt.Config and explain the rest) without storing
// settings that would have no effect.
func srtFixedMetadata() []Field {
	return []Field{
		readOnlyField("congestionType", groupSRTFixed, TypeEnum, "live", "",
			"Congestion control algorithm. gosrt validates and forces live congestion for ingest."),
		readOnlyField("transmissionType", groupSRTFixed, TypeEnum, "live", "",
			"SRT transmission type. File-transfer mode is rejected; Owncast ingests live media only."),
		readOnlyField("tooLatePacketDrop", groupSRTFixed, TypeBool, true, "",
			"Too-late packet drop is forced on by gosrt Validate for live streams."),
		readOnlyField("nakReport", groupSRTFixed, TypeBool, true, "",
			"Periodic NAK reports are forced on by gosrt Validate."),
		readOnlyField("tsbpd", groupSRTFixed, TypeBool, true, "",
			"Timestamp-based packet delivery is forced on by gosrt for live streams."),
		readOnlyField("pbKeyLen", groupSRTFixed, TypeInt, 16, "bytes",
			"Listener-side encryption key length is negotiated by the publisher; a listener-side value has no effect."),
		readOnlyField("kmRefreshRate", groupSRTFixed, TypeInt, 16777216, "packets",
			"Key-material refresh rate is driven by the publisher; the listener follows received KM packets."),
		readOnlyField("kmPreAnnounce", groupSRTFixed, TypeInt, 4096, "packets",
			"Key-material pre-announce is driven by the publisher."),
		readOnlyField("maxBandwidthBps", groupSRTFixed, TypeInt, -1, "bps",
			"Maximum sending bandwidth. The ingest listener receives media and never sends it."),
		readOnlyField("inputBandwidthBps", groupSRTFixed, TypeInt, 0, "bps",
			"Input bandwidth estimate is a sender-side option and has no effect on ingest."),
		readOnlyField("overheadBandwidthPct", groupSRTFixed, TypeInt, 25, "%",
			"Sender-side bandwidth overhead percentage; not applicable to the ingest listener."),
		readOnlyField("allowPeerIpChange", groupSRTFixed, TypeBool, false, "",
			"Fixed false for security: allows a different host to continue a publisher socket. Never exposed."),
		readOnlyField("connectionTimeoutMs", groupSRTFixed, TypeInt, 3000, "ms",
			"Handshake connection timeout is caller-side only in gosrt; the server cannot configure it."),
		readOnlyField("statsSampleIntervalMs", groupSRTFixed, TypeInt, 1000, "ms",
			"Internal live-status statistics sample cadence. Fixed to keep admin polling cheap."),
		readOnlyField("minSrtVersion", groupSRTFixed, TypeString, "1.4.1 (0x010401)", "",
			"Minimum peer SRT version. gosrt Validate rejects older peers; publishers must use SRT 1.4.1 or newer."),
		readOnlyField("packetFilter", groupSRTFixed, TypeString, "", "",
			"SRT packet filter/FEC is not exposed in this release; it must be negotiated by both peers."),
		readOnlyField("receiverBufferBytes", groupSRTFixed, TypeInt, 0, "bytes",
			"Socket receive buffer sizing is a no-op in gosrt v0.13.0; the OS default applies."),
		readOnlyField("lossMaxTTL", groupSRTFixed, TypeInt, 0, "",
			"Reorder tolerance has no receiver effect in gosrt v0.13.0; it is only echoed in statistics."),
	}
}

// NotSupportedOption documents one capability that an SRT operator may expect
// but that is unavailable (or not exposed) in the pinned gosrt version. The
// list is the single source for the operator-facing "Not supported" table
// required by spec C2; it is not served by the admin metadata endpoint
// because those entries are not settings.
type NotSupportedOption struct {
	Capability  string `json:"capability"`
	Status      string `json:"status"`
	Handling    string `json:"handling"`
	Explanation string `json:"explanation"`
}

// Not-supported status phrases, kept as constants so the operator-facing table
// uses exactly the same wording everywhere.
const (
	notSupportedStatusUnsupported = "Unsupported"
	notSupportedStatusRejected    = "Rejected by gosrt Validate"
	notSupportedStatusNotExposed  = "Not exposed"
	notSupportedStatusMitigated   = "Mitigated"
)

// SRTNotSupportedOptions is the operator-facing "Not supported" table.
func SRTNotSupportedOptions() []NotSupportedOption {
	return []NotSupportedOption{
		{"Rendezvous handshake (SRTO_RENDEZVOUS)", "Not implemented in gosrt", notSupportedStatusUnsupported, "Owncast is always the listener; encoders must use caller/listener mode."},
		{"Connection bonding / groups (SRTO_GROUPCONNECT)", notSupportedStatusRejected, notSupportedStatusUnsupported, "One publisher, one connection. Redundant-link bonding is not available."},
		{"File transfer transmission type (SRTO_TRANSTYPE=file)", notSupportedStatusRejected, notSupportedStatusUnsupported, "Owncast ingests live media only."},
		{"File Transfer Congestion Control", "Forced live by gosrt", notSupportedStatusUnsupported, "Media is live, not file transfer."},
		{"Buffer mode delivery", "TSBPD forced true", notSupportedStatusUnsupported, "Live TSBPD delivery only."},
		{"Message API / message mode", "Handshake rejects the SRT STREAM flag", notSupportedStatusUnsupported, "Stream mode (live MPEG-TS) only."},
		{"IPv6-only socket (SRTO_IPV6ONLY)", notSupportedStatusRejected, notSupportedStatusUnsupported, "Bind to an explicit IPv6 address instead."},
		{"Loss/reorder tolerance (SRTO_LOSSMAXTTL)", "No receiver logic reads it", "Unsupported (no effect)", "Not exposed; the field would be stored but ignored."},
		{"Socket receive buffer (SRTO_RCVBUF)", "Config field is a no-op", "Unsupported by default", "The OS default socket buffer applies; sysctl tuning may be needed for satellite links."},
		{"Socket send buffer (SRTO_SNDBUF)", "Config field is a no-op", notSupportedStatusUnsupported, "Sender-side only; the ingest listener does not send media."},
		{"Listener connection timeout (SRTO_CONNTIMEO)", "Caller-side only", "Unsupported as a server setting", "The publisher owns its connect timeout."},
		{"Listener crypto key length (SRTO_PBKEYLEN)", "Caller-side only", "Unsupported as a server setting", "The publisher chooses the key length; the listener accepts it."},
		{"Key-material refresh rate / pre-announce", "Sender-side only", notSupportedStatusNotExposed, "Key rotation is driven by the publisher."},
		{"Sender bandwidth limits (SRTO_MAXBW, SRTO_INPUTBW, SRTO_OHEADBW)", "Feed the sender only", notSupportedStatusNotExposed, "The ingest listener does not send media."},
		{"Sender drop delay (SRTO_SNDDROPDELAY)", "Sender-side only", notSupportedStatusNotExposed, "Sender-side drop policy."},
		{"Packet filter / FEC (SRTO_PACKETFILTER)", "Implemented in gosrt but not exposed", "Fixed empty", "FEC must be negotiated by both peers; no UI in the first release."},
		{"Subscriber/playback over SRT (m=request, subscribe)", "Owncast accepts publish only", notSupportedStatusUnsupported, "SRT is ingest only; playback stays HLS/WebRTC."},
		{"Handshake-level auth timeout hook", "No gosrt API", notSupportedStatusMitigated, "The accept callback is synchronous and O(1), with a pending-handshake cap."},
		{"Read/write deadlines on srt.Conn", "Methods exist but are no-ops", notSupportedStatusMitigated, "The service closes the connection to unblock I/O."},
		{"Belated-packet statistics", "Always zero in gosrt", "Not displayed", "Only counters gosrt actually populates are shown."},
		{"Payload-only bitrate statistic", "Byte counters include IP/UDP/SRT headers", notSupportedStatusMitigated, "Owncast counts payload bytes in the session reader."},
		{"Peer address change (SRTO_ALLOWPEERIPCHANGE)", "Available but fixed false", "Fixed", "Security hardening: never exposed."},
	}
}
