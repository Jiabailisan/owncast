package srt

import (
	"net/netip"
	"testing"
	"time"

	"github.com/owncast/owncast/models"
)

func TestBuildListenerConfigMapsExposedSettings(t *testing.T) {
	cfg := models.DefaultSRTConfig()
	cfg.ReceiverLatencyMs = 300
	cfg.PeerLatencyMs = 250
	cfg.PeerIdleTimeoutMs = 7000
	cfg.FlowControlWindowPackets = 42000
	cfg.MSS = 1400
	cfg.PayloadSize = 1200
	cfg.Passphrase = "testpassphrase123"
	cfg.DriftTracer = false
	cfg.IPTTL = 42
	cfg.IPTOS = 96
	cfg.BindAddress = "127.0.0.1"

	listenerCfg, err := BuildListenerConfig(cfg)
	if err != nil {
		t.Fatalf("BuildListenerConfig: %v", err)
	}

	if listenerCfg.ReceiverLatency != 300*time.Millisecond {
		t.Errorf("ReceiverLatency = %v, want 300ms", listenerCfg.ReceiverLatency)
	}
	if listenerCfg.PeerLatency != 250*time.Millisecond {
		t.Errorf("PeerLatency = %v, want 250ms", listenerCfg.PeerLatency)
	}
	if listenerCfg.PeerIdleTimeout != 7*time.Second {
		t.Errorf("PeerIdleTimeout = %v, want 7s", listenerCfg.PeerIdleTimeout)
	}
	if listenerCfg.FC != 42000 {
		t.Errorf("FC = %d, want 42000", listenerCfg.FC)
	}
	if listenerCfg.MSS != 1400 {
		t.Errorf("MSS = %d, want 1400", listenerCfg.MSS)
	}
	if listenerCfg.PayloadSize != 1200 {
		t.Errorf("PayloadSize = %d, want 1200", listenerCfg.PayloadSize)
	}
	if listenerCfg.Passphrase != "testpassphrase123" {
		t.Errorf("Passphrase was not mapped")
	}
	if listenerCfg.DriftTracer {
		t.Error("DriftTracer = true, want false")
	}
	if listenerCfg.IPTTL != 42 {
		t.Errorf("IPTTL = %d, want 42", listenerCfg.IPTTL)
	}
	if listenerCfg.IPTOS != 96 {
		t.Errorf("IPTOS = %d, want 96", listenerCfg.IPTOS)
	}
}

// TestBuildListenerConfigEveryExposedSettingReachesConfig mirrors the phase 0
// T11 requirement at the unit level: every exposed setting changes the gosrt
// listener Config, and no no-op/caller-side field is accidentally set.
func TestBuildListenerConfigEveryExposedSettingReachesConfig(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*models.SRTConfig)
		check  func(*testing.T, models.SRTConfig)
	}{
		{
			name:   "receiver latency",
			mutate: func(cfg *models.SRTConfig) { cfg.ReceiverLatencyMs = 500 },
			check: func(t *testing.T, cfg models.SRTConfig) {
				got, err := BuildListenerConfig(cfg)
				if err != nil {
					t.Fatalf("BuildListenerConfig: %v", err)
				}
				if got.ReceiverLatency != 500*time.Millisecond {
					t.Fatalf("ReceiverLatency = %v", got.ReceiverLatency)
				}
			},
		},
		{
			name:   "peer latency",
			mutate: func(cfg *models.SRTConfig) { cfg.PeerLatencyMs = 500 },
			check: func(t *testing.T, cfg models.SRTConfig) {
				got, err := BuildListenerConfig(cfg)
				if err != nil {
					t.Fatalf("BuildListenerConfig: %v", err)
				}
				if got.PeerLatency != 500*time.Millisecond {
					t.Fatalf("PeerLatency = %v", got.PeerLatency)
				}
			},
		},
		{
			name:   "peer idle timeout",
			mutate: func(cfg *models.SRTConfig) { cfg.PeerIdleTimeoutMs = 9000 },
			check: func(t *testing.T, cfg models.SRTConfig) {
				got, err := BuildListenerConfig(cfg)
				if err != nil {
					t.Fatalf("BuildListenerConfig: %v", err)
				}
				if got.PeerIdleTimeout != 9*time.Second {
					t.Fatalf("PeerIdleTimeout = %v", got.PeerIdleTimeout)
				}
			},
		},
		{
			name:   "flow control window",
			mutate: func(cfg *models.SRTConfig) { cfg.FlowControlWindowPackets = 12345 },
			check: func(t *testing.T, cfg models.SRTConfig) {
				got, err := BuildListenerConfig(cfg)
				if err != nil {
					t.Fatalf("BuildListenerConfig: %v", err)
				}
				if got.FC != 12345 {
					t.Fatalf("FC = %d", got.FC)
				}
			},
		},
		{
			name: "mss and payload",
			mutate: func(cfg *models.SRTConfig) {
				cfg.MSS = 1400
				cfg.PayloadSize = 1316
			},
			check: func(t *testing.T, cfg models.SRTConfig) {
				got, err := BuildListenerConfig(cfg)
				if err != nil {
					t.Fatalf("BuildListenerConfig: %v", err)
				}
				if got.MSS != 1400 || got.PayloadSize != 1316 {
					t.Fatalf("MSS/PayloadSize = %d/%d", got.MSS, got.PayloadSize)
				}
			},
		},
		{
			name:   "passphrase",
			mutate: func(cfg *models.SRTConfig) { cfg.Passphrase = "anotherpassphrase" },
			check: func(t *testing.T, cfg models.SRTConfig) {
				got, err := BuildListenerConfig(cfg)
				if err != nil {
					t.Fatalf("BuildListenerConfig: %v", err)
				}
				if got.Passphrase != "anotherpassphrase" {
					t.Fatalf("Passphrase was not mapped")
				}
			},
		},
		{
			name:   "drift tracer",
			mutate: func(cfg *models.SRTConfig) { cfg.DriftTracer = false },
			check: func(t *testing.T, cfg models.SRTConfig) {
				got, err := BuildListenerConfig(cfg)
				if err != nil {
					t.Fatalf("BuildListenerConfig: %v", err)
				}
				if got.DriftTracer {
					t.Fatalf("DriftTracer = true, want false")
				}
			},
		},
		{
			name: "ip qos",
			mutate: func(cfg *models.SRTConfig) {
				cfg.IPTTL = 32
				cfg.IPTOS = 160
			},
			check: func(t *testing.T, cfg models.SRTConfig) {
				got, err := BuildListenerConfig(cfg)
				if err != nil {
					t.Fatalf("BuildListenerConfig: %v", err)
				}
				if got.IPTTL != 32 || got.IPTOS != 160 {
					t.Fatalf("IPTTL/IPTOS = %d/%d", got.IPTTL, got.IPTOS)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := models.DefaultSRTConfig()
			cfg.BindAddress = "127.0.0.1"
			test.mutate(&cfg)
			test.check(t, cfg)
		})
	}
}

func TestBuildListenerConfigFixedAndUnsupportedFields(t *testing.T) {
	cfg := models.DefaultSRTConfig()
	cfg.BindAddress = "127.0.0.1"
	got, err := BuildListenerConfig(cfg)
	if err != nil {
		t.Fatalf("BuildListenerConfig: %v", err)
	}

	if got.Congestion != "live" {
		t.Errorf("Congestion = %q, want live", got.Congestion)
	}
	if got.TransmissionType != "live" {
		t.Errorf("TransmissionType = %q, want live", got.TransmissionType)
	}
	if !got.TSBPDMode || !got.NAKReport || !got.TooLatePacketDrop {
		t.Error("expected the live profile flags to be forced on")
	}
	if got.MessageAPI || got.GroupConnect {
		t.Error("expected unsupported modes to be disabled")
	}
	if got.IPv6Only != -1 {
		t.Errorf("IPv6Only = %d, want -1", got.IPv6Only)
	}
	if got.Latency != -1 {
		t.Errorf("Latency = %v, want -1", got.Latency)
	}
	if got.AllowPeerIpChange {
		t.Error("AllowPeerIpChange must stay false")
	}
	if got.ReceiverBufferSize != 0 || got.SendBufferSize != 0 {
		t.Error("no-op gosrt buffer fields must keep their defaults")
	}
	if got.MaxBW != -1 || got.InputBW != 0 || got.MinInputBW != 0 || got.OverheadBW != 25 {
		t.Error("sender-side bandwidth fields must keep their defaults")
	}
	if got.PBKeylen != 16 {
		t.Errorf("PBKeylen = %d, want 16", got.PBKeylen)
	}
	if got.ConnectionTimeout != 3*time.Second {
		t.Errorf("ConnectionTimeout = %v, want the gosrt default", got.ConnectionTimeout)
	}
}

func TestValidateConfigErrors(t *testing.T) {
	base := func() models.SRTConfig {
		cfg := models.DefaultSRTConfig()
		cfg.BindAddress = "127.0.0.1"
		return cfg
	}

	tests := []struct {
		name   string
		mutate func(*models.SRTConfig)
	}{
		{name: "hostname bind address", mutate: func(cfg *models.SRTConfig) { cfg.BindAddress = "localhost" }},
		{name: "port out of range", mutate: func(cfg *models.SRTConfig) { cfg.Port = 70000 }},
		{name: "unknown streamid format", mutate: func(cfg *models.SRTConfig) { cfg.StreamIDFormats = []string{"made-up"} }},
		{name: "bad allowed cidr", mutate: func(cfg *models.SRTConfig) { cfg.AllowedCIDRs = []string{"bad"} }},
		{name: "bad denied cidr", mutate: func(cfg *models.SRTConfig) { cfg.DeniedCIDRs = []string{"10.0.0.0/33"} }},
		{name: "negative rate limit", mutate: func(cfg *models.SRTConfig) { cfg.AuthFailureRateLimit = -1 }},
		{name: "receiver latency too low", mutate: func(cfg *models.SRTConfig) { cfg.ReceiverLatencyMs = 1 }},
		{name: "peer latency negative", mutate: func(cfg *models.SRTConfig) { cfg.PeerLatencyMs = -1 }},
		{name: "flow control too small", mutate: func(cfg *models.SRTConfig) { cfg.FlowControlWindowPackets = 1 }},
		{name: "mss too small", mutate: func(cfg *models.SRTConfig) { cfg.MSS = 30 }},
		{name: "payload too small", mutate: func(cfg *models.SRTConfig) { cfg.PayloadSize = 100 }},
		{name: "payload plus headers exceeds mss", mutate: func(cfg *models.SRTConfig) {
			cfg.MSS = 1400
			cfg.PayloadSize = 1400
		}},
		{name: "passphrase too short", mutate: func(cfg *models.SRTConfig) { cfg.Passphrase = "short" }},
		{name: "passphrase too long", mutate: func(cfg *models.SRTConfig) {
			cfg.Passphrase = "0123456789012345678901234567890123456789012345678901234567890123456789"
		}},
		{name: "peer idle timeout too low", mutate: func(cfg *models.SRTConfig) { cfg.PeerIdleTimeoutMs = 100 }},
		{name: "ip ttl out of range", mutate: func(cfg *models.SRTConfig) { cfg.IPTTL = 300 }},
		{name: "ip tos out of range", mutate: func(cfg *models.SRTConfig) { cfg.IPTOS = 300 }},
		{name: "ip qos with ipv6 bind", mutate: func(cfg *models.SRTConfig) {
			cfg.BindAddress = "::1"
			cfg.IPTTL = 10
		}},
		{name: "pending cap out of range", mutate: func(cfg *models.SRTConfig) { cfg.MaxPendingHandshakes = -1 }},
		{name: "debug stats out of range", mutate: func(cfg *models.SRTConfig) { cfg.DebugStatsIntervalMs = -1 }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := base()
			test.mutate(&cfg)
			if err := ValidateConfig(cfg); err == nil {
				t.Fatalf("expected a validation error for %s", test.name)
			}
		})
	}

	if err := ValidateConfig(base()); err != nil {
		t.Fatalf("default config should validate: %v", err)
	}

	ipv6 := base()
	ipv6.BindAddress = "::1"
	if err := ValidateConfig(ipv6); err != nil {
		t.Fatalf("IPv6 bind address should validate: %v", err)
	}
}

func TestListenAddress(t *testing.T) {
	cfg := models.DefaultSRTConfig()
	cfg.BindAddress = "127.0.0.1"
	cfg.Port = 8890
	if got := ListenAddress(cfg); got != "127.0.0.1:8890" {
		t.Fatalf("ListenAddress = %q", got)
	}

	cfg.BindAddress = "::1"
	if got := ListenAddress(cfg); got != "[::1]:8890" {
		t.Fatalf("ListenAddress with IPv6 = %q", got)
	}
}

func TestListenerSettingsChanged(t *testing.T) {
	base := models.DefaultSRTConfig()
	base.BindAddress = "127.0.0.1"

	live := base
	live.StreamIDFormats = []string{string(models.SRTStreamIDRawKey)}
	live.AllowedCIDRs = []string{"10.0.0.0/8"}
	live.AuthFailureRateLimit = 9
	live.LogTopics = []string{"connection"}
	if listenerSettingsChanged(base, live) {
		t.Error("live-only settings must not require a rebind")
	}

	listener := base
	listener.Passphrase = "testpassphrase123"
	if !listenerSettingsChanged(base, listener) {
		t.Error("a passphrase change must require a rebind")
	}

	listener = base
	listener.Port = 9000
	if !listenerSettingsChanged(base, listener) {
		t.Error("a port change must require a rebind")
	}
}

func TestBindAddressIPv6WithQoSIsRejected(t *testing.T) {
	cfg := models.DefaultSRTConfig()
	cfg.BindAddress = "::1"
	cfg.IPTOS = 10
	if err := ValidateConfig(cfg); err == nil {
		t.Fatal("expected ipTOS with an IPv6 bind address to be rejected")
	}

	// A valid IPv4-mapped literal is a valid IPv4 bind target.
	cfg.BindAddress = "::ffff:127.0.0.1"
	if _, err := netip.ParseAddr(cfg.BindAddress); err != nil {
		t.Fatalf("test setup: %v", err)
	}
}
