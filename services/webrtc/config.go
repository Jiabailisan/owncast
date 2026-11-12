package webrtc

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"

	"github.com/owncast/owncast/models"
	"github.com/pion/webrtc/v4"
)

// Sentinel errors returned by the service and mapped to WHEP error codes by
// the HTTP handler.
var (
	ErrInvalidConfig        = errors.New("invalid webrtc configuration")
	ErrEmbeddedTurnDisabled = errors.New("embedded TURN is not available in this release")
	ErrUnsupportedCodec     = errors.New("unsupported codec")
	ErrBadOffer             = errors.New("malformed or unsupported sdp offer")
	ErrUnsupportedMedia     = errors.New("offer does not contain a supported media format")
	ErrCodecMismatch        = errors.New("configured encoder profile does not match any offered h264 profile")
	ErrVariantNotFound      = errors.New("variant not found")
	ErrCapacity             = errors.New("webrtc capacity exhausted")
	ErrPerIPCapacity        = errors.New("webrtc per-ip capacity exhausted")
	ErrRateLimited          = errors.New("webrtc signaling rate limited")
	ErrSessionNotFound      = errors.New("webrtc session not found")
	ErrDisabled             = errors.New("webrtc delivery is disabled")
	ErrDomainMismatch       = errors.New("webrtc host or origin mismatch")
	ErrICEGatherTimeout     = errors.New("webrtc ice gathering timed out")
)

const (
	defaultBindAddress    = "0.0.0.0"
	defaultUDPMode        = "mux"
	defaultUDPPort        = 8189
	defaultUDPPortMin     = 50000
	defaultUDPPortMax     = 50100
	defaultTCPPort        = 8189
	defaultMaxViewers     = 100
	defaultRateLimit      = 30
	defaultIdleSeconds    = 30
	defaultDisconnectedMs = 5000
	defaultFailedMs       = 25000
	defaultKeepaliveMs    = 2000

	// videoPayloadType and audioPayloadType are the payload types FFmpeg is
	// told to emit. Pion rewrites them per viewer binding; the constants are
	// shared with the transcoder plan builder so the two cannot drift.
	videoPayloadType = 96
	audioPayloadType = 111

	opusClockRate  = 48000
	opusChannels   = 2
	rtpClockRate   = 90000
	minRTCPPayload = 1200

	udpModeMux   = "mux"
	udpModeRange = "range"

	networkTypeUDP4 = "udp4"
	networkTypeUDP6 = "udp6"
	networkTypeTCP4 = "tcp4"
	networkTypeTCP6 = "tcp6"
)

// canonicalH264Fmtp maps an operator-facing H.264 profile label and level to
// the SDP fmtp we advertise for the shared track. The encoder argv mapping
// lives in the transcoder package; this is only the SDP side. The advertised
// level must be at least the level of the encoded stream: a 1440p60 stream is
// level 5.1, and advertising 3.1 makes some browsers fall back to software
// decoding for an otherwise hardware-decodable stream.
func canonicalH264Fmtp(profile, level string) (string, error) {
	profileLevelID, err := canonicalH264ProfileLevelID(profile, level)
	if err != nil {
		return "", err
	}

	return "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=" + profileLevelID, nil
}

// canonicalH264ProfileLevelID returns the full SDP profile-level-id (three
// bytes) for the configured encoder profile and output level.
func canonicalH264ProfileLevelID(profile, level string) (string, error) {
	levelHex := h264LevelHex(level, profile)
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "", h264ProfileConstrainedBaseline, "constrained_baseline", h264ProfileBaseline:
		return "42e0" + levelHex, nil
	case h264ProfileMain:
		return "4d00" + levelHex, nil
	case h264ProfileHigh:
		return "6400" + levelHex, nil
	default:
		return "", fmt.Errorf("%w: unknown h264 profile %q", ErrInvalidConfig, profile)
	}
}

// h264LevelHex returns the level_idc byte as two hex digits. The configured
// level is honored when set; "auto" advertises the level required by a
// 2560x1440@60 stream (5.1) for Main/High so hardware decoders are selected.
func h264LevelHex(level, profile string) string {
	level = strings.TrimSpace(strings.ToLower(level))
	if level != "" && level != "auto" {
		var major, minor int
		if _, err := fmt.Sscanf(level, "%d.%d", &major, &minor); err == nil && major > 0 && major <= 6 && minor >= 0 && minor <= 2 {
			return fmt.Sprintf("%02x", major*10+minor)
		}
		if parsed, ok := parseH264Level(level); ok {
			return fmt.Sprintf("%02x", parsed)
		}
	}
	if strings.EqualFold(profile, h264ProfileBaseline) {
		return "1f"
	}

	return "33"
}

// h264ProfileIDC returns the profile_idc byte browsers use for this encoder
// profile family. It is used to prefer the matching H.264 payload type when a
// viewer offers several.
func h264ProfileIDC(profile string) byte {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case h264ProfileMain:
		return 0x4d
	case h264ProfileHigh:
		return 0x64
	default:
		return 0x42
	}
}

// canonicalOpusFmtp is the Opus fmtp used for the shared audio track. Browsers
// all offer minptime=10;useinbandfec=1 for WebRTC.
const canonicalOpusFmtp = "minptime=10;useinbandfec=1"

// NormalizeConfig fills in defaults for missing or nonsensical values so a
// partially populated configuration cannot bring up a half-configured engine.
// Limits with a meaningful zero (maxViewers, maxViewersPerIP) are preserved.
func NormalizeConfig(cfg models.WebRTCConfig) models.WebRTCConfig {
	normalizeNetworkConfig(&cfg)
	normalizeTimeoutConfig(&cfg)
	normalizeMediaConfig(&cfg)
	normalizeListConfig(&cfg)

	return cfg
}

func normalizeNetworkConfig(cfg *models.WebRTCConfig) {
	if cfg.BindAddress == "" {
		cfg.BindAddress = defaultBindAddress
	}
	if cfg.UDPMode == "" {
		cfg.UDPMode = defaultUDPMode
	}
	if cfg.UDPPort == 0 {
		cfg.UDPPort = defaultUDPPort
	}
	if cfg.UDPPortMin == 0 {
		cfg.UDPPortMin = defaultUDPPortMin
	}
	if cfg.UDPPortMax == 0 {
		cfg.UDPPortMax = defaultUDPPortMax
	}
	if cfg.TCPPort == 0 {
		cfg.TCPPort = defaultTCPPort
	}
	if len(cfg.NetworkTypes) == 0 {
		cfg.NetworkTypes = []string{networkTypeUDP4, networkTypeUDP6, networkTypeTCP4, networkTypeTCP6}
	}
}

func normalizeTimeoutConfig(cfg *models.WebRTCConfig) {
	if cfg.SessionIdleTimeoutSeconds <= 0 {
		cfg.SessionIdleTimeoutSeconds = defaultIdleSeconds
	}
	if cfg.ICEDisconnectedTimeoutMs <= 0 {
		cfg.ICEDisconnectedTimeoutMs = defaultDisconnectedMs
	}
	if cfg.ICEFailedTimeoutMs <= 0 {
		cfg.ICEFailedTimeoutMs = defaultFailedMs
	}
	if cfg.ICEKeepaliveIntervalMs <= 0 {
		cfg.ICEKeepaliveIntervalMs = defaultKeepaliveMs
	}
}

func normalizeMediaConfig(cfg *models.WebRTCConfig) {
	if cfg.H264Profile == "" {
		cfg.H264Profile = "constrained-baseline"
	}
	if cfg.H264Level == "" {
		cfg.H264Level = "auto"
	}
	if cfg.KeyframeIntervalSeconds <= 0 {
		cfg.KeyframeIntervalSeconds = 1
	}
	if cfg.OpusBitrateKbps <= 0 {
		cfg.OpusBitrateKbps = 96
	}
	if cfg.OpusFrameMs <= 0 {
		cfg.OpusFrameMs = 20
	}
	if cfg.RTPPacketSize == 0 {
		cfg.RTPPacketSize = minRTCPPayload
	}
	if cfg.LimitExceededBehavior == "" {
		cfg.LimitExceededBehavior = "reject"
	}
	if cfg.EmbeddedTurnPort == 0 {
		cfg.EmbeddedTurnPort = 3478
	}
}

func normalizeListConfig(cfg *models.WebRTCConfig) {
	if cfg.PublicIPs == nil {
		cfg.PublicIPs = []string{}
	}
	if cfg.InterfaceAllow == nil {
		cfg.InterfaceAllow = []string{}
	}
	if cfg.InterfaceDeny == nil {
		cfg.InterfaceDeny = []string{}
	}
	if cfg.STUNServers == nil {
		cfg.STUNServers = []string{}
	}
	if cfg.AllowedOrigins == nil {
		cfg.AllowedOrigins = []string{}
	}
	if cfg.NetworkTypes == nil {
		cfg.NetworkTypes = []string{}
	}
	if cfg.MaxViewers < 0 {
		cfg.MaxViewers = defaultMaxViewers
	}
	if cfg.MaxViewersPerIP < 0 {
		cfg.MaxViewersPerIP = 0
	}
	if cfg.SignalingRateLimit <= 0 {
		cfg.SignalingRateLimit = defaultRateLimit
	}
}

// ValidateConfig checks the restart-service configuration before the engine
// binds anything. Field-level validation for the admin API lives in the
// settings-metadata layer; this is the last-line check that protects Start.
func ValidateConfig(cfg models.WebRTCConfig) error {
	if err := validateNetworkConfig(cfg); err != nil {
		return err
	}
	if err := validateICETimeouts(cfg); err != nil {
		return err
	}
	if _, err := iceNetworkTypes(cfg); err != nil {
		return err
	}
	if _, err := canonicalH264Fmtp(cfg.H264Profile, cfg.H264Level); err != nil {
		return err
	}
	if err := validateServerConfig(cfg); err != nil {
		return err
	}
	if !cfg.TWCC && cfg.BandwidthEstimation {
		return fmt.Errorf("%w: bandwidthEstimation requires twcc", ErrInvalidConfig)
	}
	if cfg.EmbeddedTurn {
		return ErrEmbeddedTurnDisabled
	}

	return nil
}

func validateNetworkConfig(cfg models.WebRTCConfig) error {
	if cfg.UDPMode != udpModeMux && cfg.UDPMode != udpModeRange {
		return fmt.Errorf("%w: udpMode must be mux or range", ErrInvalidConfig)
	}
	if cfg.UDPMode == udpModeMux && !validPort(cfg.UDPPort) {
		return fmt.Errorf("%w: udpPort out of range", ErrInvalidConfig)
	}
	if cfg.UDPMode == udpModeRange &&
		(cfg.UDPPortMin >= cfg.UDPPortMax || !validPort(cfg.UDPPortMin) || !validPort(cfg.UDPPortMax)) {
		return fmt.Errorf("%w: udpPortMin/udpPortMax invalid", ErrInvalidConfig)
	}
	if cfg.TCPEnabled && !validPort(cfg.TCPPort) {
		return fmt.Errorf("%w: tcpPort out of range", ErrInvalidConfig)
	}
	if net.ParseIP(cfg.BindAddress) == nil {
		return fmt.Errorf("%w: bindAddress %q is not an IP address", ErrInvalidConfig, cfg.BindAddress)
	}

	return nil
}

func validateICETimeouts(cfg models.WebRTCConfig) error {
	if cfg.ICEDisconnectedTimeoutMs >= cfg.ICEFailedTimeoutMs {
		return fmt.Errorf("%w: iceFailedTimeoutMs must be greater than iceDisconnectedTimeoutMs", ErrInvalidConfig)
	}
	if cfg.ICEDisconnectedTimeoutMs <= cfg.ICEKeepaliveIntervalMs {
		return fmt.Errorf("%w: iceKeepaliveIntervalMs must be less than iceDisconnectedTimeoutMs", ErrInvalidConfig)
	}

	return nil
}

func validateServerConfig(cfg models.WebRTCConfig) error {
	for _, ip := range cfg.PublicIPs {
		if _, err := netip.ParseAddr(strings.TrimSpace(ip)); err != nil {
			return fmt.Errorf("%w: publicIPs entry %q is not an IP address", ErrInvalidConfig, ip)
		}
	}
	for _, u := range cfg.STUNServers {
		if !strings.HasPrefix(u, "stun:") && !strings.HasPrefix(u, "stuns:") {
			return fmt.Errorf("%w: stunServers entry %q must be a stun: URL", ErrInvalidConfig, u)
		}
	}

	return nil
}

// Warnings returns non-fatal configuration problems for the admin UI and the
// preflight endpoint.
func Warnings(cfg models.WebRTCConfig) []string {
	// Initialised, not nil: the API documents this as an array, and a nil slice
	// marshals to null on the healthy path.
	warnings := []string{}
	if cfg.Domain == "" && len(cfg.PublicIPs) == 0 {
		warnings = append(warnings, "webrtc_domain_unset: no domain or publicIPs configured; ICE candidates advertise local interface addresses")
	}
	if cfg.TCPEnabled && !hasTCPNetworkType(cfg.NetworkTypes) {
		warnings = append(warnings, "webrtc_tcp_without_network_type: tcpEnabled is true but networkTypes has no tcp4/tcp6 entry")
	}
	if cfg.ICELite {
		warnings = append(warnings, "webrtc_ice_lite: iceLite only works when the server has a directly reachable public IP and is incompatible with TURN relay setups")
	}
	if cfg.BandwidthEstimation {
		warnings = append(warnings, "webrtc_bandwidth_estimation_monitoring_only: bandwidth estimation is reported in the admin UI but does not adapt the encoder bitrate")
	}
	if cfg.EmbeddedTurn {
		warnings = append(warnings, "webrtc_embedded_turn_unavailable: embedded TURN is not active in this release")
	}

	return warnings
}

// RestartRequiredFields returns the configuration keys whose values only take
// effect after the service is restarted (applyMode=restart-service). The admin
// config handler uses it for the pendingRestart banner.
func RestartRequiredFields(oldCfg, newCfg models.WebRTCConfig) []string {
	var fields []string

	addIf := func(changed bool, key string) {
		if changed {
			fields = append(fields, key)
		}
	}

	addIf(oldCfg.Enabled != newCfg.Enabled, "enabled")
	addIf(oldCfg.Domain != newCfg.Domain, "domain")
	addIf(!equalStrings(oldCfg.PublicIPs, newCfg.PublicIPs), "publicIPs")
	addIf(oldCfg.BindAddress != newCfg.BindAddress, "bindAddress")
	addIf(oldCfg.UDPMode != newCfg.UDPMode, "udpMode")
	addIf(oldCfg.UDPPort != newCfg.UDPPort, "udpPort")
	addIf(oldCfg.UDPPortMin != newCfg.UDPPortMin, "udpPortMin")
	addIf(oldCfg.UDPPortMax != newCfg.UDPPortMax, "udpPortMax")
	addIf(oldCfg.TCPEnabled != newCfg.TCPEnabled, "tcpEnabled")
	addIf(oldCfg.TCPPort != newCfg.TCPPort, "tcpPort")
	addIf(!equalStrings(oldCfg.NetworkTypes, newCfg.NetworkTypes), "networkTypes")
	addIf(!equalStrings(oldCfg.InterfaceAllow, newCfg.InterfaceAllow), "interfaceAllow")
	addIf(!equalStrings(oldCfg.InterfaceDeny, newCfg.InterfaceDeny), "interfaceDeny")
	addIf(oldCfg.MDNS != newCfg.MDNS, "mdns")
	addIf(!equalStrings(oldCfg.STUNServers, newCfg.STUNServers), "stunServers")
	addIf(oldCfg.EmbeddedTurn != newCfg.EmbeddedTurn, "embeddedTurn")
	addIf(oldCfg.EmbeddedTurnPort != newCfg.EmbeddedTurnPort, "embeddedTurnPort")
	addIf(oldCfg.EmbeddedTurnRealm != newCfg.EmbeddedTurnRealm, "embeddedTurnRealm")
	addIf(oldCfg.ICELite != newCfg.ICELite, "iceLite")
	addIf(oldCfg.ICEDisconnectedTimeoutMs != newCfg.ICEDisconnectedTimeoutMs, "iceDisconnectedTimeoutMs")
	addIf(oldCfg.ICEFailedTimeoutMs != newCfg.ICEFailedTimeoutMs, "iceFailedTimeoutMs")
	addIf(oldCfg.ICEKeepaliveIntervalMs != newCfg.ICEKeepaliveIntervalMs, "iceKeepaliveIntervalMs")
	addIf(oldCfg.NACK != newCfg.NACK, "nack")
	addIf(oldCfg.TWCC != newCfg.TWCC, "twcc")
	addIf(oldCfg.BandwidthEstimation != newCfg.BandwidthEstimation, "bandwidthEstimation")
	addIf(oldCfg.RTPReorderBufferMs != newCfg.RTPReorderBufferMs, "rtpReorderBufferMs")

	sort.Strings(fields)

	return fields
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

func validPort(port int) bool {
	return port >= 1024 && port <= 65535
}

func hasTCPNetworkType(types []string) bool {
	for _, t := range types {
		if t == networkTypeTCP4 || t == networkTypeTCP6 {
			return true
		}
	}

	return false
}

// iceNetworkTypes converts the operator-facing list into Pion network types.
// ICE-TCP must be requested explicitly in Pion, so when tcpEnabled is true the
// TCP types are appended when the operator did not list them.
func iceNetworkTypes(cfg models.WebRTCConfig) ([]webrtc.NetworkType, error) {
	types := make([]webrtc.NetworkType, 0, len(cfg.NetworkTypes)+2)
	seen := map[webrtc.NetworkType]bool{}

	for _, raw := range cfg.NetworkTypes {
		nt, err := parseNetworkType(raw)
		if err != nil {
			return nil, err
		}
		if !seen[nt] {
			seen[nt] = true
			types = append(types, nt)
		}
	}

	if len(types) == 0 {
		for _, raw := range []string{"udp4", "udp6"} {
			nt, err := parseNetworkType(raw)
			if err != nil {
				return nil, err
			}
			seen[nt] = true
			types = append(types, nt)
		}
	}

	if cfg.TCPEnabled {
		for _, raw := range []string{"tcp4", "tcp6"} {
			nt, err := parseNetworkType(raw)
			if err != nil {
				return nil, err
			}
			if !seen[nt] {
				seen[nt] = true
				types = append(types, nt)
			}
		}
	}

	return types, nil
}

func parseNetworkType(raw string) (webrtc.NetworkType, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "udp4":
		return webrtc.NetworkTypeUDP4, nil
	case "udp6":
		return webrtc.NetworkTypeUDP6, nil
	case "tcp4":
		return webrtc.NetworkTypeTCP4, nil
	case "tcp6":
		return webrtc.NetworkTypeTCP6, nil
	default:
		return webrtc.NetworkTypeUnknown, fmt.Errorf("%w: unknown network type %q", ErrInvalidConfig, raw)
	}
}

// iceServersForGathering returns the STUN servers the server itself uses while
// gathering its own candidates. Viewer-facing TURN servers are deliberately
// excluded: a media-sending server must not allocate relay candidates on the
// operator's TURN server.
func iceServersForGathering(cfg models.WebRTCConfig) []webrtc.ICEServer {
	if len(cfg.STUNServers) == 0 {
		return nil
	}

	return []webrtc.ICEServer{{URLs: append([]string(nil), cfg.STUNServers...)}}
}

// viewerICEEntries returns the viewer-facing STUN/TURN entries used to build
// the Link headers and the public config iceServers array. TURN credentials
// that use a staticSecret are generated per session by the handler.
func viewerICEEntries(cfg models.WebRTCConfig) []viewerICEEntry {
	entries := make([]viewerICEEntry, 0, len(cfg.STUNServers)+len(cfg.TURNServers))
	for _, u := range cfg.STUNServers {
		entries = append(entries, viewerICEEntry{URL: u})
	}
	for _, t := range cfg.TURNServers {
		entries = append(entries, viewerICEEntry{
			URL:          t.URL,
			Username:     t.Username,
			Credential:   t.Credential,
			StaticSecret: t.StaticSecret,
		})
	}

	return entries
}

// ParsePublicIPs parses the publicIPs override into address values.
func ParsePublicIPs(cfg models.WebRTCConfig) []netip.Addr {
	addrs := make([]netip.Addr, 0, len(cfg.PublicIPs))
	for _, raw := range cfg.PublicIPs {
		addr, err := netip.ParseAddr(strings.TrimSpace(raw))
		if err == nil {
			addrs = append(addrs, addr)
		}
	}

	return addrs
}

func addrsByFamily(addrs []netip.Addr) (v4, v6 []string) {
	for _, addr := range addrs {
		addr = addr.Unmap()
		if addr.Is4() {
			v4 = append(v4, addr.String())
		} else {
			v6 = append(v6, addr.String())
		}
	}

	return v4, v6
}

// viewerICEEntry is one viewer-facing STUN/TURN entry used for Link headers and
// the public config iceServers array.
type viewerICEEntry struct {
	URL          string
	Username     string
	Credential   string
	StaticSecret string
}
