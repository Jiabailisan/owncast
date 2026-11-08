package srt

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"time"

	gosrt "github.com/datarhei/gosrt"

	"github.com/owncast/owncast/models"
)

// Bounds for the exposed SRT settings. They are enforced server-side and are
// also the source for the admin form metadata in a later phase.
const (
	minPassphraseLength = 10
	maxPassphraseLength = 64
	minPayloadSize      = 188
	maxPayloadSize      = 1456
	minMSS              = 76
	maxMSS              = 1500
	headerOverhead      = 44 // 28 bytes UDP header + 16 bytes SRT header
	minReceiverLatency  = 20
	minPeerLatency      = 20
	maxLatency          = 10000
	minPeerIdleTimeout  = 500
	maxPeerIdleTimeout  = 60000
	minFlowControl      = 32
	maxFlowControl      = 1000000
	maxHandshakePending = 1024
	maxDebugStatsMs     = 60000
	maxRateLimit        = 1000
)

// msDuration converts milliseconds from the persisted configuration to a Go
// duration.
func msDuration(ms int) time.Duration {
	return time.Duration(ms) * time.Millisecond
}

// ValidateConfig validates the persisted SRT configuration. All validation is
// server-side authoritative; field-level errors are returned to the admin API
// by the caller.
func ValidateConfig(cfg models.SRTConfig) error {
	bindAddr, err := netip.ParseAddr(cfg.BindAddress)
	if err != nil {
		return fmt.Errorf("bindAddress must be an IP address: %w", err)
	}

	if cfg.Port < 0 || cfg.Port > 65535 {
		return fmt.Errorf("port must be between 0 and 65535")
	}

	for _, format := range cfg.StreamIDFormats {
		switch models.SRTStreamIDFormat(format) {
		case models.SRTStreamIDRawKey, models.SRTStreamIDPublishPath, models.SRTStreamIDAccessControl:
		default:
			return fmt.Errorf("unknown streamid format %q", format)
		}
	}

	if err := ValidateCIDRs(cfg.AllowedCIDRs, cfg.DeniedCIDRs); err != nil {
		return err
	}

	if cfg.AuthFailureRateLimit < 0 || cfg.AuthFailureRateLimit > maxRateLimit {
		return fmt.Errorf("authFailureRateLimit must be between 0 and %d", maxRateLimit)
	}

	if cfg.ReceiverLatencyMs < minReceiverLatency || cfg.ReceiverLatencyMs > maxLatency {
		return fmt.Errorf("receiverLatencyMs must be between %d and %d", minReceiverLatency, maxLatency)
	}

	if cfg.PeerLatencyMs < minPeerLatency || cfg.PeerLatencyMs > maxLatency {
		return fmt.Errorf("peerLatencyMs must be between %d and %d", minPeerLatency, maxLatency)
	}

	if cfg.FlowControlWindowPackets < minFlowControl || cfg.FlowControlWindowPackets > maxFlowControl {
		return fmt.Errorf("flowControlWindowPackets must be between %d and %d", minFlowControl, maxFlowControl)
	}

	if cfg.MSS < minMSS || cfg.MSS > maxMSS {
		return fmt.Errorf("mss must be between %d and %d", minMSS, maxMSS)
	}

	if cfg.PayloadSize < minPayloadSize || cfg.PayloadSize > maxPayloadSize {
		return fmt.Errorf("payloadSize must be between %d and %d", minPayloadSize, maxPayloadSize)
	}
	if cfg.PayloadSize+headerOverhead > cfg.MSS {
		return fmt.Errorf("payloadSize must be at most mss minus %d bytes of protocol headers", headerOverhead)
	}

	if cfg.Passphrase != "" && (len(cfg.Passphrase) < minPassphraseLength || len(cfg.Passphrase) > maxPassphraseLength) {
		return fmt.Errorf("passphrase must be between %d and %d characters", minPassphraseLength, maxPassphraseLength)
	}

	if cfg.PeerIdleTimeoutMs < minPeerIdleTimeout || cfg.PeerIdleTimeoutMs > maxPeerIdleTimeout {
		return fmt.Errorf("peerIdleTimeoutMs must be between %d and %d", minPeerIdleTimeout, maxPeerIdleTimeout)
	}

	if cfg.IPTTL < 0 || cfg.IPTTL > 255 {
		return fmt.Errorf("ipTTL must be between 0 and 255")
	}
	if cfg.IPTOS < 0 || cfg.IPTOS > 255 {
		return fmt.Errorf("ipTOS must be between 0 and 255")
	}
	if bindAddr.Is6() && (cfg.IPTTL > 0 || cfg.IPTOS > 0) {
		return fmt.Errorf("ipTTL and ipTOS are IPv4 socket options and cannot be used with an IPv6 bindAddress")
	}

	if cfg.MaxPendingHandshakes < 1 || cfg.MaxPendingHandshakes > maxHandshakePending {
		return fmt.Errorf("maxPendingHandshakes must be between 1 and %d", maxHandshakePending)
	}

	if cfg.DebugStatsIntervalMs < 0 || cfg.DebugStatsIntervalMs > maxDebugStatsMs {
		return fmt.Errorf("debugStatsIntervalMs must be between 0 and %d", maxDebugStatsMs)
	}

	return nil
}

// BuildListenerConfig converts the persisted SRT configuration into the gosrt
// listener Config. Only settings with a proven runtime effect in the pinned
// gosrt version are mapped; caller-side and no-op gosrt fields intentionally
// keep their library defaults (see the Phase 0 dependency and design report,
// section 4). The Logger field is populated by the service at bind time.
func BuildListenerConfig(cfg models.SRTConfig) (gosrt.Config, error) {
	if err := ValidateConfig(cfg); err != nil {
		return gosrt.Config{}, err
	}

	listenerCfg := gosrt.DefaultConfig()

	// Fixed live-ingest profile: these are forced by gosrt's Validate anyway
	// and are shown read-only in the admin UI.
	listenerCfg.Congestion = "live"
	listenerCfg.TransmissionType = "live"
	listenerCfg.TSBPDMode = true
	listenerCfg.NAKReport = true
	listenerCfg.TooLatePacketDrop = true
	listenerCfg.MessageAPI = false
	listenerCfg.GroupConnect = false
	listenerCfg.IPv6Only = -1

	// Latency: keep the master Latency override disabled and set both sides
	// individually.
	listenerCfg.Latency = -1
	listenerCfg.ReceiverLatency = msDuration(cfg.ReceiverLatencyMs)
	listenerCfg.PeerLatency = msDuration(cfg.PeerLatencyMs)
	listenerCfg.PeerIdleTimeout = msDuration(cfg.PeerIdleTimeoutMs)

	// Buffers and packets.
	listenerCfg.FC = uint32(cfg.FlowControlWindowPackets) //nolint:gosec // validated range.
	listenerCfg.MSS = uint32(cfg.MSS)                     //nolint:gosec // validated range.
	listenerCfg.PayloadSize = uint32(cfg.PayloadSize)     //nolint:gosec // validated range.

	// Encryption. gosrt only validates the passphrase; the accept callback
	// enforces it with ConnRequest.SetPassphrase.
	listenerCfg.Passphrase = cfg.Passphrase

	// Network QoS (IPv4 only; zero leaves the OS default).
	listenerCfg.IPTTL = cfg.IPTTL
	listenerCfg.IPTOS = cfg.IPTOS

	// Advanced.
	listenerCfg.DriftTracer = cfg.DriftTracer

	if err := listenerCfg.Validate(); err != nil {
		return gosrt.Config{}, fmt.Errorf("invalid SRT listener config: %w", err)
	}

	return listenerCfg, nil
}

// ListenAddress returns the host:port the listener binds.
func ListenAddress(cfg models.SRTConfig) string {
	return net.JoinHostPort(cfg.BindAddress, strconv.Itoa(cfg.Port))
}

// listenerSettingsChanged reports whether applying the next configuration
// requires rebinding the gosrt listener. All other settings are applied live
// through the service's atomic runtime configuration.
func listenerSettingsChanged(current, next models.SRTConfig) bool {
	if current.Enabled != next.Enabled ||
		current.BindAddress != next.BindAddress ||
		current.Port != next.Port ||
		current.ReceiverLatencyMs != next.ReceiverLatencyMs ||
		current.PeerLatencyMs != next.PeerLatencyMs ||
		current.FlowControlWindowPackets != next.FlowControlWindowPackets ||
		current.MSS != next.MSS ||
		current.PayloadSize != next.PayloadSize ||
		current.Passphrase != next.Passphrase ||
		current.PeerIdleTimeoutMs != next.PeerIdleTimeoutMs ||
		current.IPTTL != next.IPTTL ||
		current.IPTOS != next.IPTOS ||
		current.DriftTracer != next.DriftTracer {
		return true
	}
	return false
}
