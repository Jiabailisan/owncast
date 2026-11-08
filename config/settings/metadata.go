package settings

// Group IDs used by the metadata registry. They are part of the admin API
// contract; the frontend maps them to section headings.
const (
	groupSRTListener    = "srt.listener"
	groupSRTAuth        = "srt.auth"
	groupSRTLatency     = "srt.latency"
	groupSRTBuffers     = "srt.buffers"
	groupSRTEncryption  = "srt.encryption"
	groupSRTTimeouts    = "srt.timeouts"
	groupSRTNetwork     = "srt.network"
	groupSRTDiagnostics = "srt.diagnostics"
	groupSRTAdvanced    = "srt.advanced"
	groupSRTFixed       = "srt.fixed"
	groupWebRTCGeneral  = "webrtc.general"
	groupWebRTCDomain   = "webrtc.domain" //nolint:gosec // Group label, not a credential.
	groupWebRTCLimits   = "webrtc.limits"
	groupWebRTCNetwork  = "webrtc.network"
	groupWebRTCICE      = "webrtc.ice"
	groupWebRTCMedia    = "webrtc.media"
	groupWebRTCFixed    = "webrtc.fixed"
	groupAppearance     = "appearance"
	groupOutput         = "output"
)

// srtGroups is the ordered set of SRT form sections.
var srtGroups = []Group{
	{ID: groupSRTListener, Label: "Listener"},
	{ID: groupSRTAuth, Label: "Authentication"},
	{ID: groupSRTLatency, Label: "Latency and reliability"},
	{ID: groupSRTBuffers, Label: "Buffers and packets"},
	{ID: groupSRTEncryption, Label: "Encryption"},
	{ID: groupSRTTimeouts, Label: "Timeouts"},
	{ID: groupSRTNetwork, Label: "Network QoS"},
	{ID: groupSRTDiagnostics, Label: "Diagnostics"},
	{ID: groupSRTAdvanced, Label: "Advanced"},
	{ID: groupSRTFixed, Label: "Fixed by the SRT library"},
}

// webrtcGroups is the ordered set of WebRTC form sections.
var webrtcGroups = []Group{
	{ID: groupWebRTCGeneral, Label: "General"},
	{ID: groupWebRTCDomain, Label: "Domain binding"},
	{ID: groupWebRTCLimits, Label: "Limits"},
	{ID: groupWebRTCNetwork, Label: "Network"},
	{ID: groupWebRTCICE, Label: "ICE"},
	{ID: groupWebRTCMedia, Label: "Media"},
	{ID: groupWebRTCFixed, Label: "Fixed by the browser requirements"},
}

// otherGroups is the ordered set of non-protocol form sections added in
// Phase 1 (Appearance paths and the output process model).
var otherGroups = []Group{
	{ID: groupAppearance, Label: "Appearance"},
	{ID: groupOutput, Label: "Output"},
}

// MetadataSection is the JSON container for one settings family.
type MetadataSection struct {
	Fields []Field `json:"fields"`
}

// Metadata is the full settings-metadata payload served by
// GET /api/admin/config/settings-metadata. Its JSON shape is the contract in
// docs/architecture/api-contract.md (section 1).
type Metadata struct {
	Groups     []Group         `json:"groups"`
	SRT        MetadataSection `json:"srt"`
	WebRTC     MetadataSection `json:"webrtc"`
	Appearance MetadataSection `json:"appearance"`
	Output     MetadataSection `json:"output"`
}

// All returns the complete metadata payload for the admin form.
func All() Metadata {
	return Metadata{
		Groups:     Groups(),
		SRT:        MetadataSection{Fields: SRTMetadata()},
		WebRTC:     MetadataSection{Fields: WebRTCMetadata()},
		Appearance: MetadataSection{Fields: AppearanceMetadata()},
		Output:     MetadataSection{Fields: OutputMetadata()},
	}
}

// Groups returns every form section referenced by the registry, in form
// order. It is derived from the section definitions above so a new group
// cannot be used without being listed.
func Groups() []Group {
	groups := make([]Group, 0, len(srtGroups)+len(webrtcGroups)+len(otherGroups))
	groups = append(groups, srtGroups...)
	groups = append(groups, webrtcGroups...)
	groups = append(groups, otherGroups...)
	return groups
}

// FieldByKey finds one field by its JSON key. The bool result is false when
// the key is unknown.
func FieldByKey(fields []Field, key string) (Field, bool) {
	for _, field := range fields {
		if field.Key == key {
			return field, true
		}
	}
	return Field{}, false
}

// EffectiveSRTFields returns only the fields backed by a persisted model
// field (i.e. not metadata-only fixed entries). It is what the admin form's
// value binding iterates.
func EffectiveSRTFields() []Field {
	fields := SRTMetadata()
	effective := make([]Field, 0, len(fields))
	for _, field := range fields {
		if !field.ReadOnly {
			effective = append(effective, field)
		}
	}
	return effective
}

// EffectiveWebRTCFields returns only the persisted WebRTC fields.
func EffectiveWebRTCFields() []Field {
	fields := WebRTCMetadata()
	effective := make([]Field, 0, len(fields))
	for _, field := range fields {
		if !field.ReadOnly {
			effective = append(effective, field)
		}
	}
	return effective
}
