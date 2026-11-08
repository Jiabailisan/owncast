package settings

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/owncast/owncast/models"
)

// srtContext is the default validation context for SRT tests: the real default
// WebRTC configuration plus representative web/RTMP ports.
func srtContext() ValidationContext {
	webrtc := models.DefaultWebRTCConfig()
	return ValidationContext{WebServerPort: 8080, RTMPPort: 1935, WebRTC: &webrtc}
}

// webrtcContext is the default validation context for WebRTC tests.
func webrtcContext() ValidationContext {
	srt := models.DefaultSRTConfig()
	return ValidationContext{WebServerPort: 8080, RTMPPort: 1935, SRT: &srt}
}

func requireValid(t *testing.T, errs FieldErrors) {
	t.Helper()
	if len(errs) != 0 {
		t.Fatalf("expected valid configuration, got errors: %v", errs)
	}
}

func requireFieldError(t *testing.T, errs FieldErrors, field string) {
	t.Helper()
	if !errs.Has(field) {
		t.Fatalf("expected an error on field %q, got: %v", field, errs)
	}
}

func TestSRTDefaultsAreValid(t *testing.T) {
	requireValid(t, ValidateSRT(models.DefaultSRTConfig(), srtContext()))
}

func TestWebRTCDefaultsAreValid(t *testing.T) {
	requireValid(t, ValidateWebRTC(models.DefaultWebRTCConfig(), webrtcContext()))
}

func TestCombinedDefaultsAreValid(t *testing.T) {
	requireValid(t, ValidateCombined(models.DefaultSRTConfig(), models.DefaultWebRTCConfig(),
		ValidationContext{WebServerPort: 8080, RTMPPort: 1935}))
}

type srtValidationCase struct {
	name    string
	mutate  func(*models.SRTConfig)
	context func(ValidationContext) ValidationContext
	field   string
}

func TestValidateSRTRules(t *testing.T) {
	cases := []srtValidationCase{
		{"invalid bind address", func(c *models.SRTConfig) { c.BindAddress = "localhost" }, nil, "bindAddress"},
		{"port below range", func(c *models.SRTConfig) { c.Port = 80 }, nil, "port"},
		{"port collides with web", func(c *models.SRTConfig) { c.Port = 8080 }, nil, "port"},
		{"port collides with rtmp", func(c *models.SRTConfig) { c.Port = 1935 }, nil, "port"},
		{"port collides with webrtc udp", func(c *models.SRTConfig) { c.Port = 8189 }, nil, "port"},
		{"port inside webrtc range", func(c *models.SRTConfig) { c.Port = 50050 }, nil, "port"},
		{"no streamid formats", func(c *models.SRTConfig) { c.StreamIDFormats = []string{} }, nil, "streamIdFormats"},
		{"unknown streamid format", func(c *models.SRTConfig) { c.StreamIDFormats = []string{"carrier-pigeon"} }, nil, "streamIdFormats"},
		{"auth rate limit too high", func(c *models.SRTConfig) { c.AuthFailureRateLimit = 1001 }, nil, "authFailureRateLimit"},
		{"invalid allowed CIDR", func(c *models.SRTConfig) { c.AllowedCIDRs = []string{"10.0.0.0/33"} }, nil, "allowedCIDRs"},
		{"invalid denied CIDR", func(c *models.SRTConfig) { c.DeniedCIDRs = []string{"not-a-cidr"} }, nil, "deniedCIDRs"},
		{"receiver latency too low", func(c *models.SRTConfig) { c.ReceiverLatencyMs = 10 }, nil, "receiverLatencyMs"},
		{"peer latency too high", func(c *models.SRTConfig) { c.PeerLatencyMs = 20000 }, nil, "peerLatencyMs"},
		{"fc too low", func(c *models.SRTConfig) { c.FlowControlWindowPackets = 5 }, nil, "flowControlWindowPackets"},
		{"mss too low", func(c *models.SRTConfig) { c.MSS = 40 }, nil, "mss"},
		{"payload below range", func(c *models.SRTConfig) { c.PayloadSize = 160 }, nil, "payloadSize"},
		{"payload above mss-44", func(c *models.SRTConfig) { c.MSS = 500; c.PayloadSize = 1456 }, nil, "payloadSize"},
		{"short passphrase", func(c *models.SRTConfig) { c.Passphrase = "short" }, nil, "passphrase"},
		{"long passphrase", func(c *models.SRTConfig) { c.Passphrase = strings.Repeat("a", 65) }, nil, "passphrase"},
		{"peer idle too low", func(c *models.SRTConfig) { c.PeerIdleTimeoutMs = 100 }, nil, "peerIdleTimeoutMs"},
		{"ipTTL too high", func(c *models.SRTConfig) { c.IPTTL = 300 }, nil, "ipTTL"},
		{"ipTTL with IPv6 bind", func(c *models.SRTConfig) { c.BindAddress = "::1"; c.IPTTL = 64 }, nil, "ipTTL"},
		{"ipTOS too high", func(c *models.SRTConfig) { c.IPTOS = 300 }, nil, "ipTOS"},
		{"ipTOS with IPv6 bind", func(c *models.SRTConfig) { c.BindAddress = "::1"; c.IPTOS = 16 }, nil, "ipTOS"},
		{"empty log topic", func(c *models.SRTConfig) { c.LogTopics = []string{""} }, nil, "logTopics"},
		{"log topic with whitespace", func(c *models.SRTConfig) { c.LogTopics = []string{"a b"} }, nil, "logTopics"},
		{"debug interval too high", func(c *models.SRTConfig) { c.DebugStatsIntervalMs = 70000 }, nil, "debugStatsIntervalMs"},
		{"pending handshakes zero", func(c *models.SRTConfig) { c.MaxPendingHandshakes = 0 }, nil, "maxPendingHandshakes"},
		{"pending handshakes too high", func(c *models.SRTConfig) { c.MaxPendingHandshakes = 2000 }, nil, "maxPendingHandshakes"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config := models.DefaultSRTConfig()
			tc.mutate(&config)
			context := srtContext()
			if tc.context != nil {
				context = tc.context(context)
			}
			requireFieldError(t, ValidateSRT(config, context), tc.field)
		})
	}
}

type webrtcValidationCase struct {
	name    string
	mutate  func(*models.WebRTCConfig)
	context func(ValidationContext) ValidationContext
	field   string
}

func TestValidateWebRTCRules(t *testing.T) {
	cases := []webrtcValidationCase{
		{"invalid bind address", func(c *models.WebRTCConfig) { c.BindAddress = "not-an-ip" }, nil, "bindAddress"},
		{"domain with scheme and path", func(c *models.WebRTCConfig) { c.Domain = "https://example.com/live" }, nil, "domain"},
		{"domain with port", func(c *models.WebRTCConfig) { c.Domain = "example.com:443" }, nil, "domain"},
		{"invalid public IP", func(c *models.WebRTCConfig) { c.PublicIPs = []string{"999.0.0.1"} }, nil, "publicIPs"},
		{"strict domain without target", func(c *models.WebRTCConfig) { c.StrictDomain = true }, nil, "strictDomain"},
		{"origin with path", func(c *models.WebRTCConfig) { c.AllowedOrigins = []string{"https://example.com/path"} }, nil, "allowedOrigins"},
		{"origin wildcard", func(c *models.WebRTCConfig) { c.AllowedOrigins = []string{"*"} }, nil, "allowedOrigins"},
		{"origin bad scheme", func(c *models.WebRTCConfig) { c.AllowedOrigins = []string{"ftp://example.com"} }, nil, "allowedOrigins"},
		{"max viewers negative", func(c *models.WebRTCConfig) { c.MaxViewers = -1 }, nil, "maxViewers"},
		{"max viewers too high", func(c *models.WebRTCConfig) { c.MaxViewers = 100001 }, nil, "maxViewers"},
		{"max viewers per IP too high", func(c *models.WebRTCConfig) { c.MaxViewersPerIP = 1001 }, nil, "maxViewersPerIP"},
		{"signaling rate limit zero", func(c *models.WebRTCConfig) { c.SignalingRateLimit = 0 }, nil, "signalingRateLimit"},
		{"unknown limit behavior", func(c *models.WebRTCConfig) { c.LimitExceededBehavior = "allow" }, nil, "limitExceededBehavior"},
		{"session idle too low", func(c *models.WebRTCConfig) { c.SessionIdleTimeoutSeconds = 1 }, nil, "sessionIdleTimeoutSeconds"},
		{"unknown udp mode", func(c *models.WebRTCConfig) { c.UDPMode = "auto" }, nil, "udpMode"},
		{"udp port below range", func(c *models.WebRTCConfig) { c.UDPPort = 80 }, nil, "udpPort"},
		{"udp port collision web", func(c *models.WebRTCConfig) { c.UDPPort = 8080 }, nil, "udpPort"},
		{"udp port collision rtmp", func(c *models.WebRTCConfig) { c.UDPPort = 1935 }, nil, "udpPort"},
		{"udp port collision srt", func(c *models.WebRTCConfig) { c.UDPPort = 8890 }, nil, "udpPort"},
		{"udp range inverted", func(c *models.WebRTCConfig) { c.UDPPortMin = 50100; c.UDPPortMax = 50000 }, nil, "udpPortMax"},
		{"srt port inside udp range", func(c *models.WebRTCConfig) { c.UDPPortMin = 8000; c.UDPPortMax = 9000 }, func(ctx ValidationContext) ValidationContext {
			srt := models.DefaultSRTConfig()
			ctx.SRT = &srt
			return ctx
		}, "udpPortMin"},
		{"tcp port below range", func(c *models.WebRTCConfig) { c.TCPPort = 80 }, nil, "tcpPort"},
		{"tcp port collision web", func(c *models.WebRTCConfig) { c.TCPPort = 8080 }, nil, "tcpPort"},
		{"empty network types", func(c *models.WebRTCConfig) { c.NetworkTypes = []string{} }, nil, "networkTypes"},
		{"unknown network type", func(c *models.WebRTCConfig) { c.NetworkTypes = []string{"udp4", "sctp"} }, nil, "networkTypes"},
		{"tcp network type without tcp", func(c *models.WebRTCConfig) { c.TCPEnabled = false }, nil, "networkTypes"},
		{"empty interface name", func(c *models.WebRTCConfig) { c.InterfaceAllow = []string{""} }, nil, "interfaceAllow"},
		{"invalid interface name", func(c *models.WebRTCConfig) { c.InterfaceDeny = []string{"eth 0"} }, nil, "interfaceDeny"},
		{"turn url in stun list", func(c *models.WebRTCConfig) { c.STUNServers = []string{"turn:turn.example.org:3478"} }, nil, "stunServers"},
		{"invalid stun url", func(c *models.WebRTCConfig) { c.STUNServers = []string{"not a url"} }, nil, "stunServers"},
		{"turn both credentials", func(c *models.WebRTCConfig) {
			c.TURNServers = []models.TURNServer{{URL: "turn:turn.example.org:3478", Username: "u", Credential: "c", StaticSecret: "s"}}
		}, nil, "turnServers"},
		{"turn missing credentials", func(c *models.WebRTCConfig) {
			c.TURNServers = []models.TURNServer{{URL: "turn:turn.example.org:3478"}}
		}, nil, "turnServers"},
		{"turn credential without username", func(c *models.WebRTCConfig) {
			c.TURNServers = []models.TURNServer{{URL: "turn:turn.example.org:3478", Credential: "c"}}
		}, nil, "turnServers"},
		{"turn invalid url", func(c *models.WebRTCConfig) {
			c.TURNServers = []models.TURNServer{{URL: "https://example.com", Username: "u", Credential: "c"}}
		}, nil, "turnServers"},
		{"embedded turn empty realm", func(c *models.WebRTCConfig) {
			c.EmbeddedTurn = true
			c.EmbeddedTurnRealm = ""
		}, nil, "embeddedTurnRealm"},
		{"embedded turn port collision", func(c *models.WebRTCConfig) {
			c.EmbeddedTurn = true
			c.EmbeddedTurnRealm = "example"
			c.EmbeddedTurnPort = c.UDPPort
		}, nil, "embeddedTurnPort"},
		{"ice ordering failed", func(c *models.WebRTCConfig) { c.ICEFailedTimeoutMs = 4000 }, nil, "iceFailedTimeoutMs"},
		{"ice ordering disconnected", func(c *models.WebRTCConfig) { c.ICEDisconnectedTimeoutMs = 1000; c.ICEKeepaliveIntervalMs = 2000 }, nil, "iceFailedTimeoutMs"},
		{"unknown h264 profile", func(c *models.WebRTCConfig) { c.H264Profile = "high10" }, nil, "h264Profile"},
		{"unknown h264 level", func(c *models.WebRTCConfig) { c.H264Level = "5.0" }, nil, "h264Level"},
		{"keyframe interval too low", func(c *models.WebRTCConfig) { c.KeyframeIntervalSeconds = 0.2 }, nil, "keyframeIntervalSeconds"},
		{"keyframe interval too high", func(c *models.WebRTCConfig) { c.KeyframeIntervalSeconds = 11 }, nil, "keyframeIntervalSeconds"},
		{"opus bitrate too low", func(c *models.WebRTCConfig) { c.OpusBitrateKbps = 8 }, nil, "opusBitrateKbps"},
		{"opus bitrate too high", func(c *models.WebRTCConfig) { c.OpusBitrateKbps = 300 }, nil, "opusBitrateKbps"},
		{"opus frame invalid", func(c *models.WebRTCConfig) { c.OpusFrameMs = 15 }, nil, "opusFrameMs"},
		{"rtp packet size too small", func(c *models.WebRTCConfig) { c.RTPPacketSize = 100 }, nil, "rtpPacketSize"},
		{"min bitrate above max", func(c *models.WebRTCConfig) { c.MaxBitratePerViewerKbps = 100; c.MinBitratePerViewerKbps = 200 }, nil, "minBitratePerViewerKbps"},
		{"rtp reorder too high", func(c *models.WebRTCConfig) { c.RTPReorderBufferMs = 3000 }, nil, "rtpReorderBufferMs"},
		{"bandwidth estimation without twcc", func(c *models.WebRTCConfig) {
			c.BandwidthEstimation = true
			c.TWCC = false
		}, nil, "bandwidthEstimation"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config := models.DefaultWebRTCConfig()
			tc.mutate(&config)
			context := webrtcContext()
			if tc.context != nil {
				context = tc.context(context)
			}
			requireFieldError(t, ValidateWebRTC(config, context), tc.field)
		})
	}
}

func TestValidCustomSRTConfig(t *testing.T) {
	config := models.DefaultSRTConfig()
	config.Enabled = true
	config.BindAddress = "127.0.0.1"
	config.Port = 9000
	config.StreamIDFormats = []string{string(models.SRTStreamIDRawKey), string(models.SRTStreamIDAccessControl)}
	config.AllowedCIDRs = []string{"10.0.0.0/8"}
	config.DeniedCIDRs = []string{"192.0.2.0/24"}
	config.Passphrase = "a-perfectly-long-passphrase"
	config.IPTTL = 64
	config.LogTopics = []string{"listen"}
	requireValid(t, ValidateSRT(config, srtContext()))
}

func TestValidCustomWebRTCConfig(t *testing.T) {
	config := models.DefaultWebRTCConfig()
	config.Enabled = true
	config.Domain = "live.example.com"
	config.PublicIPs = []string{"203.0.113.5", "2001:db8::5"}
	config.StrictDomain = true
	config.AllowedOrigins = []string{"https://example.com", "http://localhost:3000"}
	config.UDPMode = "range"
	config.UDPPortMin = 50000
	config.UDPPortMax = 50200
	config.NetworkTypes = []string{"udp4", "udp6"}
	config.STUNServers = []string{"stun:stun.example.org:3478"}
	config.TURNServers = []models.TURNServer{
		{URL: "turn:turn.example.org:3478", Username: "viewer", Credential: "credential"},
		{URL: "turns:turn.example.org:5349", StaticSecret: "static-secret"},
	}
	config.H264Profile = "main"
	config.H264Level = "4.1"
	config.OpusFrameMs = 40
	config.MaxBitratePerViewerKbps = 2500
	config.MinBitratePerViewerKbps = 500
	requireValid(t, ValidateWebRTC(config, webrtcContext()))
}

func TestValidateCombinedReportsCrossProtocolPortCollisions(t *testing.T) {
	srt := models.DefaultSRTConfig()
	srt.Port = 8189 // WebRTC default UDP port
	errs := ValidateCombined(srt, models.DefaultWebRTCConfig(), ValidationContext{WebServerPort: 8080, RTMPPort: 1935})
	requireFieldError(t, errs, "port")

	srt = models.DefaultSRTConfig()
	webrtc := models.DefaultWebRTCConfig()
	webrtc.UDPPort = srt.Port
	errs = ValidateCombined(srt, webrtc, ValidationContext{WebServerPort: 8080, RTMPPort: 1935})
	requireFieldError(t, errs, "udpPort")
}

func TestFieldErrorJSONShape(t *testing.T) {
	fieldErrors := FieldErrors{{Field: "port", Message: "must be between 1024 and 65535"}}
	payload, err := json.Marshal(fieldErrors)
	if err != nil {
		t.Fatalf("marshaling field errors: %v", err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshaling field errors: %v", err)
	}
	if len(decoded) != 1 || decoded[0]["field"] != "port" || decoded[0]["message"] == nil {
		t.Fatalf("unexpected field error JSON: %s", payload)
	}

	empty, err := json.Marshal(FieldErrors{})
	if err != nil {
		t.Fatalf("marshaling empty field errors: %v", err)
	}
	if string(empty) != "[]" {
		t.Errorf("empty field errors marshal to %q, want []", string(empty))
	}
}
