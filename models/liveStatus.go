package models

import "time"

// LiveStatus is the admin Live Stream Status snapshot (spec section 6).
// Nullable fields are unknown for the current protocol/configuration and must
// be rendered as "N/A", never estimated.
type LiveStatus struct {
	// State is one of live, waiting, offline, error, degraded.
	State         string     `json:"state"`
	InputProtocol string     `json:"inputProtocol"`
	StreamKeyID   string     `json:"streamKeyID,omitempty"`
	StartTime     *time.Time `json:"startTime,omitempty"`
	UptimeSeconds float64    `json:"uptimeSeconds"`

	Source  LiveSourceStatus `json:"source"`
	Ingest  LiveIngestStatus `json:"ingest"`
	Viewers LiveViewerCounts `json:"viewers"`

	FFmpeg FFmpegStatus `json:"ffmpeg"`
	WebRTC WebRTCStatus `json:"webrtc"`
	GeoIP  GeoIPStatus  `json:"geoip"`
	// Outbound describes the planned outbound streams for the active
	// stream plan. Additive to the Live Stream Status payload.
	Outbound LiveOutboundStatus `json:"outbound"`
	Warnings []StatusWarning    `json:"warnings"`
}

// LiveSourceStatus describes the inbound media source properties.
type LiveSourceStatus struct {
	Width            *int     `json:"width"`
	Height           *int     `json:"height"`
	FPS              *float64 `json:"fps"`
	VideoCodec       string   `json:"videoCodec,omitempty"`
	VideoProfile     string   `json:"videoProfile,omitempty"`
	VideoLevel       string   `json:"videoLevel,omitempty"`
	AudioCodec       string   `json:"audioCodec,omitempty"`
	VideoBitrateKbps *float64 `json:"videoBitrateKbps"`
	AudioBitrateKbps *float64 `json:"audioBitrateKbps"`
	HasBFrames       *bool    `json:"hasBFrames"`

	// Additive audio codec details (SRT via the MPEG-TS/AAC inspector). They
	// are omitted for protocols that do not carry them, keeping the RTMP
	// payload byte-identical.
	AudioProfile    string `json:"audioProfile,omitempty"`
	AudioSampleRate *int   `json:"audioSampleRate,omitempty"`
	AudioChannels   *int   `json:"audioChannels,omitempty"`
}

// LiveIngestStatus mirrors models.IngestStatus for the admin payload.
type LiveIngestStatus struct {
	SessionID     string             `json:"sessionID,omitempty"`
	RemoteAddr    string             `json:"remoteAddr,omitempty"`
	BitrateKbps   *float64           `json:"bitrateKbps"`
	PacketLossPct *float64           `json:"packetLossPct"`
	RTTMs         *float64           `json:"rttMs"`
	Retransmitted *int64             `json:"retransmittedPackets"`
	Dropped       *int64             `json:"droppedPackets"`
	Detail        map[string]float64 `json:"detail,omitempty"`
}

// LiveViewerCounts splits the viewer population by delivery protocol.
type LiveViewerCounts struct {
	Total  int `json:"total"`
	WebRTC int `json:"webrtc"`
	HLS    int `json:"hls"`
}

// FFmpegStatus lists the transcoder processes feeding the active stream.
type FFmpegStatus struct {
	ProcessModel string                `json:"processModel"`
	Processes    []FFmpegProcessStatus `json:"processes"`
}

// FFmpegProcessStatus is one supervised transcoder process.
type FFmpegProcessStatus struct {
	ID              string    `json:"id"`
	Purpose         []string  `json:"purpose"`
	PID             int       `json:"pid"`
	StartedAt       time.Time `json:"startedAt"`
	UptimeSeconds   float64   `json:"uptimeSeconds"`
	RestartCount    int       `json:"restartCount"`
	Speed           *float64  `json:"speed"`
	DroppedFrames   *int64    `json:"droppedFrames"`
	DuplicateFrames *int64    `json:"duplicateFrames"`
	CPUPercent      *float64  `json:"cpuPercent"`
	RSSBytes        *int64    `json:"rssBytes"`
	// GPU utilization for hardware encoders. Best effort: nil when the
	// encoder is not an NVIDIA NVENC encoder or nvidia-smi is unavailable.
	GPUPercent     *float64 `json:"gpuPercent"`
	GPUMemoryBytes *int64   `json:"gpuMemoryBytes"`
	GPUName        string   `json:"gpuName,omitempty"`
	// Argv is the exact executed command line, secrets redacted with ***.
	Argv []string `json:"argv,omitempty"`
	// Sections splits Argv into common/hls/webrtc/environment
	// groups for the collapsible admin FFmpeg panel (spec 6.4).
	Sections map[string][]string `json:"sections,omitempty"`
}

// WebRTCStatus reports WebRTC availability and capacity.
type WebRTCStatus struct {
	Enabled  bool            `json:"enabled"`
	Capacity WebRTCCapacity  `json:"capacity"`
	Warnings []StatusWarning `json:"warnings"`
	// Advertised is the H.264 profile-level-id the WHEP answer announces. It is
	// deliberately separate from the source's SPS profile and from the encoder's
	// output profile: the three can differ, and only this one is what the browser
	// negotiates against.
	Advertised *WebRTCAdvertisedH264 `json:"advertised,omitempty"`
	// Legs reports the resolved delivery decision for every output variant that
	// is exposed over WebRTC. It is the operator-visible answer to "why is this
	// rendition copied or transcoded": the requested mode, the mode that was
	// actually applied, the source properties the decision used, the encoder
	// settings a transcode leg will use, and every reason a copy request could
	// not be honored. Empty when no variant is exposed over WebRTC.
	Legs []WebRTCLegStatus `json:"legs"`
}

// WebRTCAdvertisedH264 is the H.264 format the WHEP answer advertises.
type WebRTCAdvertisedH264 struct {
	// Profile and Level are the configured values the answer was built from.
	Profile string `json:"profile"`
	Level   string `json:"level"`
	// ProfileLevelID is the three-byte profile-level-id in the fmtp.
	ProfileLevelID string `json:"profileLevelId"`
	// Fmtp is the full fmtp line for the video payload type.
	Fmtp string `json:"fmtp"`
}

// WebRTC delivery modes reported per leg.
const (
	WebRTCLegModeCopy      = "copy"
	WebRTCLegModeTranscode = "transcode"
	WebRTCLegModeDisabled  = "disabled"
)

// WebRTC inheritance sources: what an "inherit" (Follow HLS settings) leg
// resolved against.
const (
	WebRTCInheritFromHLSCopy      = "hls-copy"
	WebRTCInheritFromHLSTranscode = "hls-transcode"
	WebRTCInheritFromNoHLS        = "no-hls"
)

// WebRTCLegStatus is one resolved WebRTC delivery decision for an output
// variant. Values are the planned settings, not measured output.
type WebRTCLegStatus struct {
	// VariantIndex is the configured variant position (the WHEP variant index).
	VariantIndex int `json:"variantIndex"`
	// Name is the operator-facing variant name.
	Name string `json:"name,omitempty"`

	// RequestedMode is the configured mode: inherit, transcode or copy.
	RequestedMode string `json:"requestedMode"`
	// InheritedFrom is set only for RequestedMode=inherit and names what the
	// inheritance resolved against (hls-copy, hls-transcode, no-hls).
	InheritedFrom string `json:"inheritedFrom,omitempty"`
	// Mode is the mode that was actually applied: copy, transcode or disabled.
	Mode string `json:"mode"`

	// Source echoes the inbound properties the decision was made from.
	Source WebRTCLegSource `json:"source"`

	// Encoder settings, set for transcode legs only.
	VideoBitrateKbps int    `json:"videoBitrateKbps,omitempty"`
	GOPFrames        int    `json:"gopFrames,omitempty"`
	Framerate        int    `json:"framerate,omitempty"`
	H264Profile      string `json:"h264Profile,omitempty"`
	// ScaledWidth/ScaledHeight are the transcode leg's output size, zero when
	// it keeps the source size.
	ScaledWidth  int `json:"scaledWidth,omitempty"`
	ScaledHeight int `json:"scaledHeight,omitempty"`

	// OutputBitrateKbps is the video bitrate measured at this rendition's RTP
	// leg (average since the leg opened), i.e. what the encoder actually
	// produced. Nil while the leg is not open or has not run for a second.
	OutputBitrateKbps *float64 `json:"outputBitrateKbps"`

	// IneligibilityReasons lists every rule a copy request failed, prefixed
	// with a stable code (webrtc_copy_ineligible_*).
	IneligibilityReasons []string `json:"ineligibilityReasons"`
	// Warnings are operator-facing notes (fallback applied, unverified copy, ...).
	Warnings []string `json:"warnings"`
}

// WebRTCLegSource is the inbound property set a leg decision used. Zero values
// mean "unknown"; the eligibility rules are conservative about unknown values.
type WebRTCLegSource struct {
	Protocol     string   `json:"protocol,omitempty"`
	VideoCodec   string   `json:"videoCodec,omitempty"`
	VideoProfile string   `json:"videoProfile,omitempty"`
	VideoLevel   string   `json:"videoLevel,omitempty"`
	HasBFrames   *bool    `json:"hasBFrames"`
	Width        int      `json:"width,omitempty"`
	Height       int      `json:"height,omitempty"`
	FPS          *float64 `json:"fps"`
	// VideoBitrateKbps is the source video bitrate the decision saw, nil when
	// the protocol could not measure or report it.
	VideoBitrateKbps *float64 `json:"videoBitrateKbps"`
	// BitrateSource names where VideoBitrateKbps came from: measured (SRT PES
	// statistics) or declared (RTMP onMetaData), empty when unknown.
	BitrateSource string `json:"bitrateSource,omitempty"`
}

// WebRTCCapacity is the viewer capacity gate state.
type WebRTCCapacity struct {
	Used  int `json:"used"`
	Limit int `json:"limit"`
	// Reserved is the number of capacity reservations in flight, i.e. WHEP
	// requests that have been admitted but have not produced a session yet.
	Reserved int `json:"reserved"`
	// MaxViewersPerIP is the configured per-address limit, 0 when unlimited.
	MaxViewersPerIP int `json:"maxViewersPerIP"`
	// RejectedTotal counts viewer requests refused by the gate since start.
	RejectedTotal uint64 `json:"rejectedTotal"`
}

// GeoIPStatus reports the offline GeoIP database state.
type GeoIPStatus struct {
	Configured   bool                  `json:"configured"`
	DatabaseType string                `json:"databaseType,omitempty"`
	BuildDate    string                `json:"buildDate,omitempty"`
	Sources      []GeoIPDatabaseSource `json:"sources,omitempty"`
}

// StatusWarning is an admin-visible degraded/error condition.
type StatusWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// LiveViewer is one row of the admin viewer table (spec 6.3).
type LiveViewer struct {
	ClientID       string `json:"clientID"`
	IPAddress      string `json:"ipAddress"`
	UserAgent      string `json:"userAgent,omitempty"`
	Country        string `json:"country,omitempty"`
	Region         string `json:"region,omitempty"`
	City           string `json:"city,omitempty"`
	LocationApprox bool   `json:"locationApproximate,omitempty"`
	Protocol       string `json:"protocol"`
	// ICETransport is the protocol of the selected ICE candidate pair for
	// WebRTC rows ("udp" or "tcp"), empty when unknown or not applicable.
	ICETransport    string    `json:"iceTransport,omitempty"`
	BitrateKbps     *float64  `json:"bitrateKbps"`
	Resolution      string    `json:"resolution,omitempty"`
	FPS             *float64  `json:"fps"`
	PacketLossPct   *float64  `json:"packetLossPct"`
	JitterMs        *float64  `json:"jitterMs"`
	ConnectedAt     time.Time `json:"connectedAt"`
	DurationSeconds float64   `json:"durationSeconds"`
	Source          string    `json:"source"`

	// NameToken is the chat token the session reported, resolved to a name by the
	// consumer. It never leaves the server: it is excluded from every response.
	NameToken string `json:"-"`

	// Player-reported fields. Empty when the player sends no heartbeat details.
	Username  string   `json:"username,omitempty"`
	Quality   string   `json:"quality,omitempty"`
	LatencyMs *float64 `json:"latencyMs"`
}

// LiveViewersPage is a page of the admin viewer table.
type LiveViewersPage struct {
	Viewers  []LiveViewer `json:"viewers"`
	Total    int          `json:"total"`
	Page     int          `json:"page"`
	PageSize int          `json:"pageSize"`
}

// GeoIPDatabaseSource describes one loaded GeoIP database file (admin status).
type GeoIPDatabaseSource struct {
	Role         string `json:"role"`
	Configured   bool   `json:"configured"`
	Path         string `json:"path,omitempty"`
	DatabaseType string `json:"databaseType,omitempty"`
	BuildDate    string `json:"buildDate,omitempty"`
	HasCity      bool   `json:"hasCity,omitempty"`
	Error        string `json:"error,omitempty"`
}

// LiveOutboundStatus describes the planned outbound streams for the active
// stream plan (additive to the Live Stream Status payload). Values are the
// configured/planned targets, not measured throughput: a passthrough stream
// uses the inbound source bitrate when it is known and nil (= N/A) otherwise.
type LiveOutboundStatus struct {
	Streams []LiveOutboundStream `json:"streams"`
}

// LiveOutboundStream is one planned outbound stream (one output variant).
type LiveOutboundStream struct {
	VariantIndex     int      `json:"variantIndex"`
	Name             string   `json:"name,omitempty"`
	VideoEncoder     string   `json:"videoEncoder,omitempty"`
	VideoBitrateKbps *float64 `json:"videoBitrateKbps"`
	AudioEncoder     string   `json:"audioEncoder,omitempty"`
	AudioBitrateKbps *float64 `json:"audioBitrateKbps"`
	Protocols        []string `json:"protocols"`
}
