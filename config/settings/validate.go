package settings

import (
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/owncast/owncast/models"
)

// FieldError is one server-side validation failure, addressed to a single
// configuration field. The JSON shape matches the admin API contract:
//
//	{"field": "port", "message": "must not collide with the web port"}
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Error implements the error interface.
func (e FieldError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// FieldErrors is the list of validation failures for one configuration save.
// It is JSON-marshaled directly as the API's fieldErrors array; an empty list
// (not null) means valid. Use Has in tests and handlers.
type FieldErrors []FieldError

// Error implements the error interface.
func (fe FieldErrors) Error() string {
	if len(fe) == 0 {
		return ""
	}
	parts := make([]string, 0, len(fe))
	for _, fieldError := range fe {
		parts = append(parts, fieldError.Error())
	}
	return strings.Join(parts, "; ")
}

// Has reports whether the given field has a validation error.
func (fe FieldErrors) Has(field string) bool {
	for _, fieldError := range fe {
		if fieldError.Field == field {
			return true
		}
	}
	return false
}

// add appends one field error.
func (fe *FieldErrors) addf(field, format string, args ...any) {
	*fe = append(*fe, FieldError{Field: field, Message: fmt.Sprintf(format, args...)})
}

// ValidationContext carries the other configuration values a single setting
// cannot be validated without. Ports are compared only when greater than zero;
// nil protocol configs skip the corresponding cross-protocol collision checks.
type ValidationContext struct {
	// WebServerPort is the configured public HTTP port (0 = unknown/skip).
	WebServerPort int
	// RTMPPort is the configured RTMP ingest port (0 = unknown/skip).
	RTMPPort int
	// SRT is the current SRT configuration, for WebRTC collision checks.
	SRT *models.SRTConfig
	// WebRTC is the current WebRTC configuration, for SRT collision checks.
	WebRTC *models.WebRTCConfig
}

var (
	hostnamePattern      = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*\.?$`)
	allowedOriginSchemes = map[string]bool{"http": true, "https": true}
	stunSchemes          = map[string]bool{"stun": true, "stuns": true}
	turnSchemes          = map[string]bool{"turn": true, "turns": true}
)

// ValidateSRT is the authoritative server-side validator for the SRT ingest
// configuration. It returns one FieldError per failed rule, addressed to the
// configuration JSON key (so the admin form can render it next to the input).
// An empty result means the configuration is valid.
func ValidateSRT(config models.SRTConfig, ctx ValidationContext) FieldErrors {
	var errs FieldErrors
	validateSRTListener(&errs, config, ctx)
	validateSRTAuth(&errs, config)
	validateSRTLatencyAndBuffers(&errs, config)
	validateSRTEncryptionAndTimeouts(&errs, config)
	validateSRTNetworkQoS(&errs, config)
	validateSRTDiagnosticsAndAdvanced(&errs, config)
	return errs
}

// validateSRTListener validates the bind address, port range and collisions.
func validateSRTListener(errs *FieldErrors, config models.SRTConfig, ctx ValidationContext) {
	if _, err := netip.ParseAddr(config.BindAddress); err != nil {
		errs.addf("bindAddress", "must be an IPv4 or IPv6 address literal")
	}
	if config.Port < 1024 || config.Port > 65535 {
		errs.addf("port", "must be between 1024 and 65535")
	}
	addPortCollision(errs, "port", config.Port, ctx.WebServerPort, "web server")
	// SRT is UDP and the RTMP listener is TCP, so the same numeric port is a
	// legal coexistence (operators commonly reuse 1935 for SRT too). Only real
	// socket conflicts are reported: WebRTC's UDP port/range and embedded TURN.
	_ = ctx.RTMPPort
	if ctx.WebRTC == nil {
		return
	}
	addPortCollision(errs, "port", config.Port, ctx.WebRTC.UDPPort, "WebRTC UDP")
	if config.Port >= ctx.WebRTC.UDPPortMin && config.Port <= ctx.WebRTC.UDPPortMax {
		errs.addf("port", "must not fall inside the WebRTC UDP port range (%d-%d)", ctx.WebRTC.UDPPortMin, ctx.WebRTC.UDPPortMax)
	}
	if ctx.WebRTC.EmbeddedTurn {
		addPortCollision(errs, "port", config.Port, ctx.WebRTC.EmbeddedTurnPort, "embedded TURN")
	}
}

// validateSRTAuth validates streamid formats, CIDR lists and the auth rate
// limit.
func validateSRTAuth(errs *FieldErrors, config models.SRTConfig) {
	if config.RequireStreamID && len(config.StreamIDFormats) == 0 {
		errs.addf("streamIdFormats", "must contain at least one format when requireStreamId is enabled")
	}
	for _, format := range config.StreamIDFormats {
		if !isKnownEnumValue(SRTMetadata(), "streamIdFormats", format) {
			errs.addf("streamIdFormats", "unsupported streamid format %q", format)
		}
	}
	if config.AuthFailureRateLimit < 0 || config.AuthFailureRateLimit > 1000 {
		errs.addf("authFailureRateLimit", "must be between 0 and 1000 failures per minute")
	}
	validateCIDRList(errs, "allowedCIDRs", config.AllowedCIDRs)
	validateCIDRList(errs, "deniedCIDRs", config.DeniedCIDRs)
}

// validateSRTLatencyAndBuffers validates latency and packet-size bounds.
func validateSRTLatencyAndBuffers(errs *FieldErrors, config models.SRTConfig) {
	checkIntRange(errs, "receiverLatencyMs", config.ReceiverLatencyMs, 20, 10000)
	checkIntRange(errs, "peerLatencyMs", config.PeerLatencyMs, 20, 10000)
	checkIntRange(errs, "flowControlWindowPackets", config.FlowControlWindowPackets, 32, 1000000)
	checkIntRange(errs, "mss", config.MSS, 76, 1500)
	checkIntRange(errs, "payloadSize", config.PayloadSize, 188, 1456)
	if config.MSS >= 44 && config.PayloadSize > config.MSS-44 {
		errs.addf("payloadSize", "must be at most mss - 44 (%d), got %d", config.MSS-44, config.PayloadSize)
	}
}

// validateSRTEncryptionAndTimeouts validates the passphrase length and the
// peer idle timeout.
func validateSRTEncryptionAndTimeouts(errs *FieldErrors, config models.SRTConfig) {
	if len(config.Passphrase) != 0 && (len(config.Passphrase) < 10 || len(config.Passphrase) > 64) {
		errs.addf("passphrase", "must be 10 to 64 characters when set")
	}
	checkIntRange(errs, "peerIdleTimeoutMs", config.PeerIdleTimeoutMs, 500, 60000)
}

// validateSRTNetworkQoS validates the IPv4-only socket options.
func validateSRTNetworkQoS(errs *FieldErrors, config models.SRTConfig) {
	bindAddr, bindErr := netip.ParseAddr(config.BindAddress)
	ipv4Bind := bindErr == nil && bindAddr.Is4()
	if config.IPTTL != 0 {
		if config.IPTTL < 1 || config.IPTTL > 255 {
			errs.addf("ipTTL", "must be between 1 and 255, or 0 to leave the OS default")
		}
		if !ipv4Bind {
			errs.addf("ipTTL", "is only supported with an IPv4 bind address")
		}
	}
	if config.IPTOS < 0 || config.IPTOS > 255 {
		errs.addf("ipTOS", "must be between 0 and 255")
	}
	if config.IPTOS != 0 && !ipv4Bind {
		errs.addf("ipTOS", "is only supported with an IPv4 bind address")
	}
}

// validateSRTDiagnosticsAndAdvanced validates log topics and connection caps.
func validateSRTDiagnosticsAndAdvanced(errs *FieldErrors, config models.SRTConfig) {
	for _, topic := range config.LogTopics {
		if topic == "" || strings.ContainsAny(topic, " \t\n") {
			errs.addf("logTopics", "contains an invalid log topic %q", topic)
		}
	}
	checkIntRange(errs, "debugStatsIntervalMs", config.DebugStatsIntervalMs, 0, 60000)
	checkIntRange(errs, "maxPendingHandshakes", config.MaxPendingHandshakes, 1, 1024)
}

// ValidateWebRTC is the authoritative server-side validator for the WebRTC
// delivery configuration. See ValidateSRT for the error shape.
func ValidateWebRTC(config models.WebRTCConfig, ctx ValidationContext) FieldErrors {
	var errs FieldErrors
	validateWebRTCGeneral(&errs, config)
	validateWebRTCLimits(&errs, config)
	validateWebRTCNetwork(&errs, config, ctx)
	validateWebRTCICE(&errs, config, ctx)
	validateWebRTCMedia(&errs, config)
	return errs
}

// validateWebRTCGeneral validates addresses, the domain, public IPs and CORS
// origins.
func validateWebRTCGeneral(errs *FieldErrors, config models.WebRTCConfig) {
	if _, err := netip.ParseAddr(config.BindAddress); err != nil {
		errs.addf("bindAddress", "must be an IPv4 or IPv6 address literal")
	}
	if err := validateDomain(config.Domain); err != nil {
		errs.addf("domain", "%s", err.Error())
	}
	for _, ip := range config.PublicIPs {
		if _, err := netip.ParseAddr(ip); err != nil {
			errs.addf("publicIPs", "contains an invalid IP address %q", ip)
		}
	}
	if config.StrictDomain && config.Domain == "" && len(config.PublicIPs) == 0 {
		errs.addf("strictDomain", "requires a domain or at least one public IP address")
	}
	for _, origin := range config.AllowedOrigins {
		if err := validateOrigin(origin); err != nil {
			errs.addf("allowedOrigins", "%s", err.Error())
		}
	}
}

// validateWebRTCLimits validates viewer limits and signaling budgets.
func validateWebRTCLimits(errs *FieldErrors, config models.WebRTCConfig) {
	checkIntRange(errs, "maxViewers", config.MaxViewers, 0, 100000)
	checkIntRange(errs, "maxViewersPerIP", config.MaxViewersPerIP, 0, 1000)
	checkIntRange(errs, "signalingRateLimit", config.SignalingRateLimit, 1, 10000)
	if !isKnownEnumValue(WebRTCMetadata(), "limitExceededBehavior", config.LimitExceededBehavior) {
		errs.addf("limitExceededBehavior", "must be one of %s", enumList(WebRTCMetadata(), "limitExceededBehavior"))
	}
	checkIntRange(errs, "sessionIdleTimeoutSeconds", config.SessionIdleTimeoutSeconds, 5, 600)
}

// validateWebRTCNetwork validates port mode, port ranges, port collisions and
// ICE network types.
func validateWebRTCNetwork(errs *FieldErrors, config models.WebRTCConfig, ctx ValidationContext) {
	if !isKnownEnumValue(WebRTCMetadata(), "udpMode", config.UDPMode) {
		errs.addf("udpMode", "must be one of %s", enumList(WebRTCMetadata(), "udpMode"))
	}
	validateWebRTCNetworkPorts(errs, config, ctx)
	if len(config.NetworkTypes) == 0 {
		errs.addf("networkTypes", "must contain at least one network type")
	}
	for _, networkType := range config.NetworkTypes {
		if !isKnownEnumValue(WebRTCMetadata(), "networkTypes", networkType) {
			errs.addf("networkTypes", "unsupported network type %q", networkType)
			continue
		}
		if strings.HasPrefix(networkType, "tcp") && !config.TCPEnabled {
			errs.addf("networkTypes", "TCP network type %q requires tcpEnabled", networkType)
		}
	}
	validateInterfaceNames(errs, "interfaceAllow", config.InterfaceAllow)
	validateInterfaceNames(errs, "interfaceDeny", config.InterfaceDeny)
}

// validateWebRTCNetworkPorts validates port ranges and every port collision.
func validateWebRTCNetworkPorts(errs *FieldErrors, config models.WebRTCConfig, ctx ValidationContext) {
	checkIntRange(errs, "udpPort", config.UDPPort, 1024, 65535)
	checkIntRange(errs, "udpPortMin", config.UDPPortMin, 1024, 65535)
	checkIntRange(errs, "udpPortMax", config.UDPPortMax, 1024, 65535)
	if config.UDPPortMin >= config.UDPPortMax {
		errs.addf("udpPortMax", "must be greater than udpPortMin (%d)", config.UDPPortMin)
	}
	checkIntRange(errs, "tcpPort", config.TCPPort, 1024, 65535)

	srtPort := 0
	if ctx.SRT != nil {
		srtPort = ctx.SRT.Port
	}
	addPortCollision(errs, "udpPort", config.UDPPort, ctx.WebServerPort, "web server")
	addPortCollision(errs, "udpPort", config.UDPPort, ctx.RTMPPort, "RTMP")
	addPortCollision(errs, "udpPort", config.UDPPort, srtPort, "SRT")
	addPortCollision(errs, "tcpPort", config.TCPPort, ctx.WebServerPort, "web server")
	addPortCollision(errs, "tcpPort", config.TCPPort, ctx.RTMPPort, "RTMP")
	if srtPort != 0 && srtPort >= config.UDPPortMin && srtPort <= config.UDPPortMax {
		errs.addf("udpPortMin", "UDP port range (%d-%d) must not include the SRT port %d", config.UDPPortMin, config.UDPPortMax, srtPort)
	}
}

// validateWebRTCICE validates STUN/TURN entries, embedded TURN and the ICE
// timeout ordering.
func validateWebRTCICE(errs *FieldErrors, config models.WebRTCConfig, ctx ValidationContext) {
	for _, stunServer := range config.STUNServers {
		if err := validateICEURL(stunServer, stunSchemes, "stun"); err != nil {
			errs.addf("stunServers", "%s", err.Error())
		}
	}
	for index, turnServer := range config.TURNServers {
		if err := validateTURNServer(turnServer); err != nil {
			errs.addf("turnServers", "entry %d: %s", index+1, err.Error())
		}
	}
	checkIntRange(errs, "embeddedTurnPort", config.EmbeddedTurnPort, 1024, 65535)
	validateEmbeddedTurn(errs, config, ctx)
	checkIntRange(errs, "iceDisconnectedTimeoutMs", config.ICEDisconnectedTimeoutMs, 1000, 120000)
	checkIntRange(errs, "iceFailedTimeoutMs", config.ICEFailedTimeoutMs, 2000, 300000)
	checkIntRange(errs, "iceKeepaliveIntervalMs", config.ICEKeepaliveIntervalMs, 500, 60000)
	if config.ICEFailedTimeoutMs <= config.ICEDisconnectedTimeoutMs ||
		config.ICEDisconnectedTimeoutMs <= config.ICEKeepaliveIntervalMs {
		errs.addf("iceFailedTimeoutMs", "timeouts must satisfy iceFailedTimeoutMs > iceDisconnectedTimeoutMs > iceKeepaliveIntervalMs (got %d, %d, %d)",
			config.ICEFailedTimeoutMs, config.ICEDisconnectedTimeoutMs, config.ICEKeepaliveIntervalMs)
	}
}

// validateEmbeddedTurn validates the optional embedded TURN settings.
func validateEmbeddedTurn(errs *FieldErrors, config models.WebRTCConfig, ctx ValidationContext) {
	if !config.EmbeddedTurn {
		return
	}
	if strings.TrimSpace(config.EmbeddedTurnRealm) == "" {
		errs.addf("embeddedTurnRealm", "is required when the embedded TURN server is enabled")
	}
	srtPort := 0
	if ctx.SRT != nil {
		srtPort = ctx.SRT.Port
	}
	addPortCollision(errs, "embeddedTurnPort", config.EmbeddedTurnPort, config.UDPPort, "WebRTC UDP")
	addPortCollision(errs, "embeddedTurnPort", config.EmbeddedTurnPort, srtPort, "SRT")
	if config.EmbeddedTurnPort >= config.UDPPortMin && config.EmbeddedTurnPort <= config.UDPPortMax {
		errs.addf("embeddedTurnPort", "must not fall inside the WebRTC UDP port range (%d-%d)", config.UDPPortMin, config.UDPPortMax)
	}
}

// validateWebRTCMedia validates codec enums, media ranges and feedback flags.
func validateWebRTCMedia(errs *FieldErrors, config models.WebRTCConfig) {
	if !isKnownEnumValue(WebRTCMetadata(), "h264Profile", config.H264Profile) {
		errs.addf("h264Profile", "must be one of %s", enumList(WebRTCMetadata(), "h264Profile"))
	}
	if !isKnownEnumValue(WebRTCMetadata(), "h264Level", config.H264Level) {
		errs.addf("h264Level", "must be one of %s", enumList(WebRTCMetadata(), "h264Level"))
	}
	if config.KeyframeIntervalSeconds < 0.5 || config.KeyframeIntervalSeconds > 10 {
		errs.addf("keyframeIntervalSeconds", "must be between 0.5 and 10")
	}
	checkIntRange(errs, "opusBitrateKbps", config.OpusBitrateKbps, 16, 256)
	if !isKnownEnumValue(WebRTCMetadata(), "opusFrameMs", strconv.Itoa(config.OpusFrameMs)) {
		errs.addf("opusFrameMs", "must be one of %s", enumList(WebRTCMetadata(), "opusFrameMs"))
	}
	checkIntRange(errs, "rtpPacketSize", config.RTPPacketSize, 500, 1400)
	checkIntRange(errs, "maxBitratePerViewerKbps", config.MaxBitratePerViewerKbps, 0, 100000)
	checkIntRange(errs, "minBitratePerViewerKbps", config.MinBitratePerViewerKbps, 0, 100000)
	if config.MaxBitratePerViewerKbps > 0 && config.MinBitratePerViewerKbps > 0 &&
		config.MinBitratePerViewerKbps > config.MaxBitratePerViewerKbps {
		errs.addf("minBitratePerViewerKbps", "must be less than or equal to maxBitratePerViewerKbps (%d)", config.MaxBitratePerViewerKbps)
	}
	checkIntRange(errs, "rtpReorderBufferMs", config.RTPReorderBufferMs, 0, 100)
	if config.BandwidthEstimation && !config.TWCC {
		errs.addf("bandwidthEstimation", "requires twcc to consume transport feedback")
	}
}

// ValidateCombined validates both protocol configurations and their
// cross-protocol port collisions in one pass. Use it when saving either
// configuration because a port change in one can invalidate the other.
func ValidateCombined(srt models.SRTConfig, webrtc models.WebRTCConfig, ctx ValidationContext) FieldErrors {
	ctx.SRT = &srt
	ctx.WebRTC = &webrtc
	errs := ValidateSRT(srt, ctx)
	errs = append(errs, ValidateWebRTC(webrtc, ctx)...)
	return errs
}

// addPortCollision appends an error when port matches otherPort.
func addPortCollision(errs *FieldErrors, field string, port, otherPort int, otherName string) {
	if port == 0 || otherPort == 0 || port != otherPort {
		return
	}
	errs.addf(field, "must not collide with the %s port (%d)", otherName, otherPort)
}

// checkIntRange appends an error when value is outside [min, max].
func checkIntRange(errs *FieldErrors, field string, value, min, max int) {
	if value < min || value > max {
		errs.addf(field, "must be between %d and %d", min, max)
	}
}

// isKnownEnumValue reports whether value is listed in the metadata enum for
// key. The metadata registry is the single source of truth for enum values.
func isKnownEnumValue(fields []Field, key, value string) bool {
	field, ok := FieldByKey(fields, key)
	if !ok || len(field.Enum) == 0 {
		return true
	}
	for _, candidate := range field.Enum {
		if candidate == value {
			return true
		}
	}
	return false
}

// enumList returns the metadata enum values for key, formatted for an error
// message.
func enumList(fields []Field, key string) string {
	field, ok := FieldByKey(fields, key)
	if !ok {
		return ""
	}
	return strings.Join(field.Enum, ", ")
}

// validateCIDRList appends one error per invalid CIDR in values.
func validateCIDRList(errs *FieldErrors, field string, values []string) {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != value {
			errs.addf(field, "contains surrounding whitespace in %q", value)
			continue
		}
		if _, err := netip.ParsePrefix(trimmed); err != nil {
			errs.addf(field, "contains an invalid CIDR %q", value)
		}
	}
}

// validateInterfaceNames rejects empty and whitespace-containing interface
// names. Existence is checked by the Phase 3 preflight, not here, because a
// saved configuration must stay valid across machines.
func validateInterfaceNames(errs *FieldErrors, field string, values []string) {
	for _, value := range values {
		if value == "" || strings.ContainsAny(value, " \t\n") {
			errs.addf(field, "contains an invalid interface name %q", value)
		}
	}
}

// ValidateDomain reports whether domain is acceptable as the WebRTC domain
// binding: empty (auto), an IP literal, or an FQDN. It is exported for the
// admin "test domain" endpoint so the API and the config validator agree.
func ValidateDomain(domain string) error {
	return validateDomain(domain)
}

// validateDomain accepts empty values (auto), IP literals, and FQDNs. Ports,
// schemes, paths and spaces are rejected so the value can be used both for DNS
// resolution and for building the public WHEP URL.
func validateDomain(domain string) error {
	if domain == "" {
		return nil
	}
	if _, err := netip.ParseAddr(domain); err == nil {
		return nil
	}
	if len(domain) > 253 {
		return fmt.Errorf("domain must be at most 253 characters")
	}
	if strings.ContainsAny(domain, "/:@ \t\n") {
		return fmt.Errorf("domain must be a bare hostname without a scheme, port or path")
	}
	if !hostnamePattern.MatchString(domain) {
		return fmt.Errorf("domain %q is not a valid FQDN", domain)
	}
	return nil
}

// validateOrigin requires an explicit http(s) origin with no path, query,
// fragment, user info or wildcard.
func validateOrigin(origin string) error {
	if strings.Contains(origin, "*") {
		return fmt.Errorf("origin %q must not contain a wildcard", origin)
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return fmt.Errorf("origin %q is not a valid URL", origin)
	}
	if !allowedOriginSchemes[strings.ToLower(parsed.Scheme)] {
		return fmt.Errorf("origin %q must use http or https", origin)
	}
	if parsed.Host == "" {
		return fmt.Errorf("origin %q must include a host", origin)
	}
	if parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("origin %q must be an origin without a path or query", origin)
	}
	return nil
}

// validateICEURL validates a STUN or TURN URL against the accepted schemes.
func validateICEURL(value string, schemes map[string]bool, kind string) error {
	if value == "" {
		return fmt.Errorf("%s URL must not be empty", kind)
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("%s URL %q is not valid", kind, value)
	}
	if !schemes[strings.ToLower(parsed.Scheme)] {
		return fmt.Errorf("%s URL %q must use the %s: scheme", kind, value, kind)
	}
	if parsed.Opaque == "" && parsed.Host == "" {
		return fmt.Errorf("%s URL %q is missing a host", kind, value)
	}
	return nil
}

// validateTURNServer validates one structured TURN entry: a TURN URL plus
// exactly one credential style (username+credential or staticSecret).
func validateTURNServer(server models.TURNServer) error {
	if err := validateICEURL(server.URL, turnSchemes, "turn"); err != nil {
		return err
	}
	if server.StaticSecret != "" && server.Credential != "" {
		return fmt.Errorf("must not set both credential and staticSecret")
	}
	if server.StaticSecret == "" && server.Credential == "" {
		return fmt.Errorf("must set credential or staticSecret")
	}
	if server.Credential != "" && server.Username == "" {
		return fmt.Errorf("must set username when credential is used")
	}
	return nil
}
