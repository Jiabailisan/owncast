package models

// Delivery and ingest protocol identifiers used by the live status payloads.
const (
	LiveProtocolRTMP   = "rtmp"
	LiveProtocolSRT    = "srt"
	LiveProtocolHLS    = "hls"
	LiveProtocolWebRTC = "webrtc"
)

// LiveStatusState values.
const (
	LiveStateLive    = "live"
	LiveStateWaiting = "waiting"
	LiveStateOffline = "offline"
	LiveStateError   = "error"
	LiveStateDegraded = "degraded"
)
