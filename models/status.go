package models

import "github.com/owncast/owncast/utils"

// Status represents the status of the system.
type Status struct {
	LastConnectTime    *utils.NullTime `json:"lastConnectTime"`
	LastDisconnectTime *utils.NullTime `json:"lastDisconnectTime"`

	VersionNumber         string `json:"versionNumber"`
	StreamTitle           string `json:"streamTitle"`
	ViewerCount           int    `json:"viewerCount"`
	OverallMaxViewerCount int    `json:"overallMaxViewerCount"`
	SessionMaxViewerCount int    `json:"sessionMaxViewerCount"`

	Online bool `json:"online"`

	// InputProtocol is the protocol of the active ingest session ("rtmp" or
	// "srt"), empty while offline. Additive public field (spec 7.2).
	InputProtocol string `json:"inputProtocol,omitempty"`

	// Additive public stream metering for the viewer Stats for nerds panel.
	// Bitrates are the source ingest values in kbps (the same values the admin
	// Live Stream Status shows); VideoPipeline labels the active delivery
	// pipeline ("hls") and is empty while offline.
	VideoBitrate  int    `json:"videoBitrate,omitempty"`
	AudioBitrate  int    `json:"audioBitrate,omitempty"`
	VideoPipeline string `json:"videoPipeline,omitempty"`
}
