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

	FFmpeg   FFmpegStatus    `json:"ffmpeg"`
	WebRTC   WebRTCStatus    `json:"webrtc"`
	GeoIP    GeoIPStatus     `json:"geoip"`
	Warnings []StatusWarning `json:"warnings"`
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
	// Argv is the exact executed command line, secrets redacted with ***.
	Argv []string `json:"argv,omitempty"`
	// Sections splits Argv into input/video/audio/output/other/environment
	// groups for the collapsible admin FFmpeg panel (spec 6.4).
	Sections map[string][]string `json:"sections,omitempty"`
}

// WebRTCStatus reports WebRTC availability and capacity.
type WebRTCStatus struct {
	Enabled  bool            `json:"enabled"`
	Capacity WebRTCCapacity  `json:"capacity"`
	Warnings []StatusWarning `json:"warnings"`
}

// WebRTCCapacity is the viewer capacity gate state.
type WebRTCCapacity struct {
	Used  int `json:"used"`
	Limit int `json:"limit"`
}

// GeoIPStatus reports the offline GeoIP database state.
type GeoIPStatus struct {
	Configured   bool   `json:"configured"`
	DatabaseType string `json:"databaseType,omitempty"`
	BuildDate    string `json:"buildDate,omitempty"`
}

// StatusWarning is an admin-visible degraded/error condition.
type StatusWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// LiveViewer is one row of the admin viewer table (spec 6.3).
type LiveViewer struct {
	ClientID        string    `json:"clientID"`
	IPAddress       string    `json:"ipAddress"`
	UserAgent       string    `json:"userAgent,omitempty"`
	Country         string    `json:"country,omitempty"`
	Region          string    `json:"region,omitempty"`
	City            string    `json:"city,omitempty"`
	LocationApprox  bool      `json:"locationApproximate,omitempty"`
	Protocol        string    `json:"protocol"`
	BitrateKbps     *float64  `json:"bitrateKbps"`
	Resolution      string    `json:"resolution,omitempty"`
	FPS             *float64  `json:"fps"`
	PacketLossPct   *float64  `json:"packetLossPct"`
	JitterMs        *float64  `json:"jitterMs"`
	ConnectedAt     time.Time `json:"connectedAt"`
	DurationSeconds float64   `json:"durationSeconds"`
	Source          string    `json:"source"`
}

// LiveViewersPage is a page of the admin viewer table.
type LiveViewersPage struct {
	Viewers  []LiveViewer `json:"viewers"`
	Total    int          `json:"total"`
	Page     int          `json:"page"`
	PageSize int          `json:"pageSize"`
}
