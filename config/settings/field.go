// Package settings defines the authoritative metadata registry and server-side
// validation for the Phase 1 SRT and WebRTC configuration.
//
// The registry is the single source of truth for:
//
//   - the admin form (field list, groups, types, ranges, enums, defaults,
//     units, descriptions, apply mode and secret flags),
//   - the API documentation generated from the same data,
//   - server-side validation (config/settings.ValidateSRT / ValidateWebRTC),
//   - tests that prove every persisted configuration field has metadata and
//     that the metadata defaults cannot drift from the model defaults.
//
// It deliberately contains no persistence logic: persistence/configrepository
// reads and writes models.SRTConfig / models.WebRTCConfig and calls the
// normalizers in this package to fill stale values with defaults.
package settings

import (
	"fmt"
	"strings"
	"unicode"
)

// FieldType is the value type of one persisted setting. These names are part
// of the admin API contract (docs/architecture/api-contract.md) and must not be
// renamed without updating the frontend types.
type FieldType string

const (
	// TypeBool is a boolean setting.
	TypeBool FieldType = "bool"
	// TypeInt is an integer setting.
	TypeInt FieldType = "int"
	// TypeFloat is a floating point setting.
	TypeFloat FieldType = "float"
	// TypeString is a free-form string setting.
	TypeString FieldType = "string"
	// TypeStringList is a list of strings.
	TypeStringList FieldType = "stringList"
	// TypeEnum is a string constrained to the values in Field.Enum.
	TypeEnum FieldType = "enum"
	// TypeCIDRList is a list of CIDR prefixes (IPv4 and/or IPv6).
	TypeCIDRList FieldType = "cidrList"
	// TypeSecret is a write-only secret string. The admin API returns a
	// "<field>Set": bool instead of the value, and the value must never be
	// logged.
	TypeSecret FieldType = "secret"
	// TypeTurnServerList is a list of models.TURNServer entries. It is an
	// extension of the contract type list because the WebRTC TURN
	// configuration is structured (per-entry credentials) rather than a flat
	// string list; each entry's credential/staticSecret are write-only.
	TypeTurnServerList FieldType = "turnServerList"
)

// ApplyMode describes when a saved setting takes effect.
//
// This mirrors the master spec section 4.1:
//
//   - live: read on the next decision (per request/session/connection),
//   - restart-service: the owning service must be restarted (listener rebind);
//   - restart-stream: applies when the next stream starts (plan build),
//   - restart-server: requires a process restart.
type ApplyMode string

const (
	// ApplyLive takes effect immediately, without restarting anything.
	ApplyLive ApplyMode = "live"
	// ApplyRestartService requires restarting only the owning service.
	ApplyRestartService ApplyMode = "restart-service"
	// ApplyRestartStream applies at the next stream start.
	ApplyRestartStream ApplyMode = "restart-stream"
	// ApplyRestartServer requires a full server restart.
	ApplyRestartServer ApplyMode = "restart-server"
	// ApplyNone marks metadata-only (read-only/fixed) entries that have no
	// apply behavior because they are not persisted. It is an extension of
	// the contract enum used only with ReadOnly fields.
	ApplyNone ApplyMode = "n/a"
)

// Group is a named section of the admin configuration form.
type Group struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Field is one persisted (or read-only/fixed) setting descriptor.
//
// JSON field names are the admin API contract. Every field is always
// serialized, including null for absent Min/Max/Enum, so the frontend can
// treat the shape as stable.
type Field struct {
	// Key is the configuration JSON key (the model field's json tag for
	// persisted fields). It is stable API surface.
	Key string `json:"key"`
	// Label is the short human-readable form label. Descriptions carry
	// the longer help text.
	Label string `json:"label"`
	// Group is the Group.ID this setting belongs to.
	Group string `json:"group"`
	// Type is the value type.
	Type FieldType `json:"type"`
	// Default is the default value; its Go type matches Type.
	Default any `json:"default"`
	// Min is the inclusive lower bound for numeric types, or nil.
	Min *float64 `json:"min"`
	// Max is the inclusive upper bound for numeric types, or nil.
	Max *float64 `json:"max"`
	// Enum lists the accepted values for enum fields (string forms; integer
	// enums are represented as decimal strings), or nil.
	Enum []string `json:"enum"`
	// Unit is the operator-facing unit ("ms", "kbps", "port", ...) or "".
	Unit string `json:"unit"`
	// Description is the localizable explanation shown in the admin form.
	Description string `json:"description"`
	// Secret marks write-only values. The API must never return the value,
	// and no log statement may include it.
	Secret bool `json:"secret"`
	// ApplyMode is when the value takes effect.
	ApplyMode ApplyMode `json:"applyMode"`
	// ReadOnly marks a fixed option that cannot be changed from the UI. Such
	// entries are metadata-only: their key is not present in the persisted
	// config model (see SRTMetadata/WebRTCMetadata fixed groups).
	ReadOnly bool `json:"readOnly"`
}

// labelOverrides maps keys whose humanized form would be wrong or ugly.
var labelOverrides = map[string]string{
	"mss":                       "MSS",
	"ipTTL":                     "IP TTL",
	"ipTOS":                     "IP TOS",
	"mdns":                      "mDNS",
	"iceLite":                   "ICE lite",
	"h264Profile":               "H.264 profile",
	"h264Level":                 "H.264 level",
	"opusFEC":                   "Opus FEC",
	"opusDTX":                   "Opus DTX",
	"nack":                      "NACK",
	"twcc":                      "TWCC",
	"rtpPacketSize":             "RTP packet size",
	"rtpReorderBufferMs":        "RTP reorder buffer",
	"udpMode":                   "UDP mode",
	"udpPort":                   "UDP port",
	"udpPortMin":                "UDP port range start",
	"udpPortMax":                "UDP port range end",
	"tcpEnabled":                "ICE-TCP enabled",
	"tcpPort":                   "ICE-TCP port",
	"stunServers":               "STUN servers",
	"turnServers":               "TURN servers",
	"embeddedTurn":              "Embedded TURN",
	"embeddedTurnPort":          "Embedded TURN port",
	"embeddedTurnRealm":         "Embedded TURN realm",
	"streamIdFormats":           "Stream ID formats",
	"requireStreamId":           "Require stream ID",
	"allowedCIDRs":              "Allowed CIDRs",
	"deniedCIDRs":               "Denied CIDRs",
	"authFailureRateLimit":      "Auth failure rate limit",
	"receiverLatencyMs":         "Receiver latency",
	"peerLatencyMs":             "Peer latency",
	"peerIdleTimeoutMs":         "Peer idle timeout",
	"debugStatsIntervalMs":      "Debug stats interval",
	"maxPendingHandshakes":      "Max pending handshakes",
	"fallbackToHls":             "Fall back to HLS",
	"maxViewersPerIP":           "Max viewers per IP",
	"signalingRateLimit":        "Signaling rate limit",
	"sessionIdleTimeoutSeconds": "Session idle timeout",
	"keyframeIntervalSeconds":   "Keyframe interval",
	"opusBitrateKbps":           "Opus bitrate",
	"opusFrameMs":               "Opus frame duration",
	"maxBitratePerViewerKbps":   "Max bitrate per viewer",
	"minBitratePerViewerKbps":   "Min bitrate per viewer",
	"customPlayerPath":          "Custom player module path",
	"customFrontendDir":         "Custom frontend directory",
}

// fieldLabel humanizes a field key for a short form label.
func fieldLabel(key string) string {
	if label, ok := labelOverrides[key]; ok {
		return label
	}
	runes := []rune(key)
	var builder strings.Builder
	for index := 0; index < len(runes); index++ {
		if index > 0 && unicode.IsUpper(runes[index]) {
			previous := runes[index-1]
			nextIsLower := index+1 < len(runes) && unicode.IsLower(runes[index+1])
			if unicode.IsLower(previous) || unicode.IsDigit(previous) || nextIsLower {
				builder.WriteRune(' ')
			}
		}
		builder.WriteRune(runes[index])
	}
	label := builder.String()
	label = strings.ToUpper(label[:1]) + label[1:]
	return label
}

// numberBounds returns the pointer pair used by Min/Max.
func numberBounds(min, max float64) (*float64, *float64) {
	return &min, &max
}

// intField builds an integer Field with bounds.
func intField(key, group string, def, min, max int, unit, description string, apply ApplyMode) Field {
	lower, upper := numberBounds(float64(min), float64(max))
	return Field{
		Key:         key,
		Label:       fieldLabel(key),
		Group:       group,
		Type:        TypeInt,
		Default:     def,
		Min:         lower,
		Max:         upper,
		Unit:        unit,
		Description: description,
		ApplyMode:   apply,
	}
}

// floatField builds a float Field with bounds.
func floatField(key, group string, def, min, max float64, unit, description string, apply ApplyMode) Field {
	lower, upper := numberBounds(min, max)
	return Field{
		Key:         key,
		Label:       fieldLabel(key),
		Group:       group,
		Type:        TypeFloat,
		Default:     def,
		Min:         lower,
		Max:         upper,
		Unit:        unit,
		Description: description,
		ApplyMode:   apply,
	}
}

// boolField builds a boolean Field.
func boolField(key, group string, def bool, description string, apply ApplyMode) Field {
	return Field{
		Key:         key,
		Label:       fieldLabel(key),
		Group:       group,
		Type:        TypeBool,
		Default:     def,
		Description: description,
		ApplyMode:   apply,
	}
}

// stringField builds a free-form string Field.
func stringField(key, group, def, unit, description string, apply ApplyMode) Field {
	return Field{
		Key:         key,
		Label:       fieldLabel(key),
		Group:       group,
		Type:        TypeString,
		Default:     def,
		Unit:        unit,
		Description: description,
		ApplyMode:   apply,
	}
}

// stringListField builds a []string Field.
func stringListField(key, group string, def []string, description string, apply ApplyMode) Field {
	return Field{
		Key:         key,
		Label:       fieldLabel(key),
		Group:       group,
		Type:        TypeStringList,
		Default:     def,
		Description: description,
		ApplyMode:   apply,
	}
}

// cidrListField builds a CIDR list Field.
func cidrListField(key, group string, def []string, description string, apply ApplyMode) Field {
	return Field{
		Key:         key,
		Label:       fieldLabel(key),
		Group:       group,
		Type:        TypeCIDRList,
		Default:     def,
		Description: description,
		ApplyMode:   apply,
	}
}

// enumField builds a string enum Field.
func enumField(key, group, def string, values []string, description string, apply ApplyMode) Field {
	return Field{
		Key:         key,
		Label:       fieldLabel(key),
		Group:       group,
		Type:        TypeEnum,
		Default:     def,
		Enum:        values,
		Description: description,
		ApplyMode:   apply,
	}
}

// secretField builds a write-only secret Field.
func secretField(key, group, description string, apply ApplyMode) Field {
	return Field{
		Key:         key,
		Label:       fieldLabel(key),
		Group:       group,
		Type:        TypeSecret,
		Default:     "",
		Description: description,
		Secret:      true,
		ApplyMode:   apply,
	}
}

// readOnlyField builds a metadata-only fixed option entry. These are not part
// of the persisted configuration model: they document gosrt/browser values
// that are fixed by the library or by the protocol and are shown read-only in
// the admin UI (spec C2). They never appear in validation or persistence.
func readOnlyField(key, group string, typ FieldType, def any, unit, description string) Field {
	return Field{
		Key:         key,
		Label:       fieldLabel(key),
		Group:       group,
		Type:        typ,
		Default:     def,
		Unit:        unit,
		Description: description,
		ApplyMode:   ApplyNone,
		ReadOnly:    true,
	}
}

// String implements fmt.Stringer for log and error messages.
func (f Field) String() string {
	return fmt.Sprintf("%s (%s)", f.Key, f.Group)
}
