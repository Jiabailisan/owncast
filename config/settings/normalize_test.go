package settings

import (
	"reflect"
	"testing"

	"github.com/owncast/owncast/models"
)

func TestWithSRTDefaultsFillsInvalidZeros(t *testing.T) {
	normalized := WithSRTDefaults(models.SRTConfig{})

	assertEqualForTest(t, normalized.BindAddress, "0.0.0.0")
	assertEqualForTest(t, normalized.Port, 8890)
	assertEqualForTest(t, len(normalized.StreamIDFormats), len(models.AllSRTStreamIDFormats()))
	assertEqualForTest(t, normalized.ReceiverLatencyMs, 120)
	assertEqualForTest(t, normalized.PeerLatencyMs, 120)
	assertEqualForTest(t, normalized.FlowControlWindowPackets, 25600)
	assertEqualForTest(t, normalized.MSS, 1500)
	assertEqualForTest(t, normalized.PayloadSize, 1316)
	assertEqualForTest(t, normalized.PeerIdleTimeoutMs, 5000)
	assertEqualForTest(t, normalized.MaxPendingHandshakes, 32)
	if normalized.AllowedCIDRs == nil || normalized.DeniedCIDRs == nil || normalized.LogTopics == nil {
		t.Error("empty list fields must be non-nil after normalization")
	}
}

func TestWithSRTDefaultsPreservesLegalZeros(t *testing.T) {
	config := models.SRTConfig{
		AuthFailureRateLimit: 0,
		DebugStatsIntervalMs: 0,
		IPTTL:                0,
		IPTOS:                0,
		Enabled:              false,
		EnforcedEncryption:   false,
		DriftTracer:          false,
	}
	normalized := WithSRTDefaults(config)
	assertEqualForTest(t, normalized.AuthFailureRateLimit, 0)
	assertEqualForTest(t, normalized.DebugStatsIntervalMs, 0)
	assertEqualForTest(t, normalized.IPTTL, 0)
	assertEqualForTest(t, normalized.IPTOS, 0)
	assertEqualForTest(t, normalized.Enabled, false)
	assertEqualForTest(t, normalized.EnforcedEncryption, false)
	assertEqualForTest(t, normalized.DriftTracer, false)
}

func TestWithSRTDefaultsPreservesValidConfig(t *testing.T) {
	config := models.DefaultSRTConfig()
	config.Enabled = true
	config.Port = 9000
	config.BindAddress = "127.0.0.1"
	config.ReceiverLatencyMs = 300
	config.Passphrase = "a-perfectly-long-passphrase"
	assertEqualForTest(t, WithSRTDefaults(config), config)
}

func TestWithWebRTCDefaultsFillsInvalidZeros(t *testing.T) {
	normalized := WithWebRTCDefaults(models.WebRTCConfig{})

	assertEqualForTest(t, normalized.BindAddress, "0.0.0.0")
	assertEqualForTest(t, normalized.UDPMode, "mux")
	assertEqualForTest(t, normalized.UDPPort, 8189)
	assertEqualForTest(t, normalized.UDPPortMin, 50000)
	assertEqualForTest(t, normalized.UDPPortMax, 50100)
	assertEqualForTest(t, normalized.TCPPort, 8189)
	assertEqualForTest(t, normalized.LimitExceededBehavior, "reject")
	assertEqualForTest(t, normalized.SignalingRateLimit, 30)
	assertEqualForTest(t, normalized.SessionIdleTimeoutSeconds, 30)
	assertEqualForTest(t, normalized.H264Profile, "constrained-baseline")
	assertEqualForTest(t, normalized.H264Level, "auto")
	assertEqualForTest(t, normalized.KeyframeIntervalSeconds, 1.0)
	assertEqualForTest(t, normalized.OpusBitrateKbps, 96)
	assertEqualForTest(t, normalized.OpusFrameMs, 20)
	assertEqualForTest(t, normalized.RTPPacketSize, 1200)
	assertEqualForTest(t, normalized.EmbeddedTurnPort, 3478)
	assertEqualForTest(t, normalized.EmbeddedTurnRealm, "owncast")
	assertEqualForTest(t, normalized.ICEDisconnectedTimeoutMs, 5000)
	assertEqualForTest(t, normalized.ICEFailedTimeoutMs, 25000)
	assertEqualForTest(t, normalized.ICEKeepaliveIntervalMs, 2000)
	if normalized.PublicIPs == nil || normalized.AllowedOrigins == nil ||
		normalized.InterfaceAllow == nil || normalized.InterfaceDeny == nil ||
		normalized.STUNServers == nil || normalized.TURNServers == nil {
		t.Error("empty list fields must be non-nil after normalization")
	}
}

func TestWithWebRTCDefaultsPreservesLegalZeros(t *testing.T) {
	config := models.WebRTCConfig{
		MaxViewers:              0,
		MaxViewersPerIP:         0,
		MaxBitratePerViewerKbps: 0,
		MinBitratePerViewerKbps: 0,
		RTPReorderBufferMs:      0,
		Enabled:                 false,
		NACK:                    false,
		OpusFEC:                 false,
	}
	normalized := WithWebRTCDefaults(config)
	assertEqualForTest(t, normalized.MaxViewers, 0)
	assertEqualForTest(t, normalized.MaxViewersPerIP, 0)
	assertEqualForTest(t, normalized.MaxBitratePerViewerKbps, 0)
	assertEqualForTest(t, normalized.MinBitratePerViewerKbps, 0)
	assertEqualForTest(t, normalized.RTPReorderBufferMs, 0)
	assertEqualForTest(t, normalized.Enabled, false)
	assertEqualForTest(t, normalized.NACK, false)
	assertEqualForTest(t, normalized.OpusFEC, false)
}

func TestWithWebRTCDefaultsNetworkTypesWithoutTCP(t *testing.T) {
	normalized := WithWebRTCDefaults(models.WebRTCConfig{TCPEnabled: false})
	assertEqualForTest(t, normalized.NetworkTypes, []string{"udp4", "udp6"})

	normalized = WithWebRTCDefaults(models.WebRTCConfig{TCPEnabled: true})
	assertEqualForTest(t, normalized.NetworkTypes, models.DefaultWebRTCConfig().NetworkTypes)
}

func TestWithWebRTCDefaultsFillsEmptyNetworkTypes(t *testing.T) {
	// A persisted empty list is indistinguishable from unset; it must be filled or
	// tcpEnabled silently does nothing.
	empty := []string{}
	cfg := models.WebRTCConfig{TCPEnabled: true, NetworkTypes: empty}

	normalized := WithWebRTCDefaults(cfg)
	assertEqualForTest(t, normalized.NetworkTypes, models.DefaultWebRTCConfig().NetworkTypes)

	cfg = models.WebRTCConfig{TCPEnabled: false, NetworkTypes: []string{}}
	normalized = WithWebRTCDefaults(cfg)
	assertEqualForTest(t, normalized.NetworkTypes, []string{"udp4", "udp6"})
}

func TestWithWebRTCDefaultsAddsTCPTypesWhenTCPEnabled(t *testing.T) {
	// UDP-only types with tcpEnabled is the inconsistent combination found on a live
	// install: the warning said TCP did nothing, and it did nothing.
	cfg := models.WebRTCConfig{TCPEnabled: true, NetworkTypes: []string{"udp4", "udp6"}}

	normalized := WithWebRTCDefaults(cfg)
	assertEqualForTest(t, normalized.NetworkTypes, []string{"udp4", "udp6", "tcp4", "tcp6"})

	// Disabling TCP must not add them back.
	cfg.TCPEnabled = false
	normalized = WithWebRTCDefaults(cfg)
	assertEqualForTest(t, normalized.NetworkTypes, []string{"udp4", "udp6"})
}

func TestNormalizedZeroConfigsAreValid(t *testing.T) {
	requireValid(t, ValidateSRT(WithSRTDefaults(models.SRTConfig{}), srtContext()))
	requireValid(t, ValidateWebRTC(WithWebRTCDefaults(models.WebRTCConfig{}), webrtcContext()))
}

func TestWithWebRTCDefaultsPreservesValidConfig(t *testing.T) {
	config := models.DefaultWebRTCConfig()
	config.Enabled = true
	config.Domain = "live.example.com"
	config.TURNServers = []models.TURNServer{{URL: "turn:turn.example.org:3478", Username: "viewer", Credential: "credential"}}
	assertEqualForTest(t, WithWebRTCDefaults(config), config)
}

func assertEqualForTest(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}
