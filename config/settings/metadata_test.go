package settings

import (
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/owncast/owncast/models"
)

// modelJSONTags returns the JSON keys of a models configuration struct in
// declaration order.
func modelJSONTags(t *testing.T, value any) []string {
	t.Helper()
	typ := reflect.TypeOf(value)
	keys := make([]string, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		key, _, _ := strings.Cut(tag, ",")
		keys = append(keys, key)
	}
	return keys
}

// persistedKeys returns the keys of the fields that are backed by the model.
func persistedKeys(fields []Field) []string {
	keys := make([]string, 0, len(fields))
	for _, field := range fields {
		if !field.ReadOnly {
			keys = append(keys, field.Key)
		}
	}
	return keys
}

func TestSRTMetadataCoversEveryPersistedField(t *testing.T) {
	modelKeys := modelJSONTags(t, models.SRTConfig{})
	metadataKeys := persistedKeys(SRTMetadata())
	if !slices.Equal(modelKeys, metadataKeys) {
		t.Fatalf("SRT metadata does not match the model field order\n model: %v\nmetadata: %v", modelKeys, metadataKeys)
	}
}

// fixedModelFields are model fields that intentionally have no writable
// setting because no runtime argv can apply them. They are documented as
// read-only metadata entries instead (master spec rule 3: no fake settings).
var fixedModelFields = map[string]bool{
	"opusFEC": true,
	"opusDTX": true,
}

func TestWebRTCMetadataCoversEveryPersistedField(t *testing.T) {
	modelKeys := make([]string, 0, len(modelJSONTags(t, models.WebRTCConfig{})))
	for _, key := range modelJSONTags(t, models.WebRTCConfig{}) {
		if fixedModelFields[key] {
			continue
		}
		modelKeys = append(modelKeys, key)
	}
	metadataKeys := persistedKeys(WebRTCMetadata())
	// WebRTC metadata is ordered by form group (fallbackToHls belongs to the
	// General section), so completeness is a set assertion; uniqueness and
	// group references are asserted separately.
	sortedModelKeys := slices.Clone(modelKeys)
	sortedMetadataKeys := slices.Clone(metadataKeys)
	slices.Sort(sortedModelKeys)
	slices.Sort(sortedMetadataKeys)
	if !slices.Equal(sortedModelKeys, sortedMetadataKeys) {
		t.Fatalf("WebRTC metadata does not cover exactly the model fields\n model: %v\nmetadata: %v", modelKeys, metadataKeys)
	}
}

func TestSRTMetadataHasReadOnlyFixedOptions(t *testing.T) {
	fixed := 0
	for _, field := range SRTMetadata() {
		if field.ReadOnly {
			fixed++
			if field.Group != groupSRTFixed {
				t.Errorf("read-only field %q must be in group %q, got %q", field.Key, groupSRTFixed, field.Group)
			}
			if field.ApplyMode != ApplyNone {
				t.Errorf("read-only field %q must use %q apply mode, got %q", field.Key, ApplyNone, field.ApplyMode)
			}
		}
	}
	if fixed == 0 {
		t.Error("expected metadata-only fixed SRT options to be present")
	}
}

func TestWebRTCMetadataHasReadOnlyFixedOptions(t *testing.T) {
	fixed := 0
	for _, field := range WebRTCMetadata() {
		if field.ReadOnly {
			fixed++
			if field.Group != groupWebRTCFixed {
				t.Errorf("read-only field %q must be in group %q, got %q", field.Key, groupWebRTCFixed, field.Group)
			}
			if field.ApplyMode != ApplyNone {
				t.Errorf("read-only field %q must use %q apply mode, got %q", field.Key, ApplyNone, field.ApplyMode)
			}
		}
	}
	if fixed == 0 {
		t.Error("expected metadata-only fixed WebRTC options to be present")
	}
}

func TestMetadataKeysAreUniquePerSection(t *testing.T) {
	for name, fields := range map[string][]Field{
		"srt":        SRTMetadata(),
		"webrtc":     WebRTCMetadata(),
		"appearance": AppearanceMetadata(),
		"output":     OutputMetadata(),
	} {
		seen := map[string]bool{}
		for _, field := range fields {
			if seen[field.Key] {
				t.Errorf("%s metadata contains duplicate key %q", name, field.Key)
			}
			seen[field.Key] = true
		}
	}
}

func TestReadOnlyMetadataKeysAreNotPersisted(t *testing.T) {
	srtModelKeys := map[string]bool{}
	for _, key := range modelJSONTags(t, models.SRTConfig{}) {
		srtModelKeys[key] = true
	}
	webrtcModelKeys := map[string]bool{}
	for _, key := range modelJSONTags(t, models.WebRTCConfig{}) {
		webrtcModelKeys[key] = true
	}

	for _, field := range SRTMetadata() {
		if field.ReadOnly && srtModelKeys[field.Key] {
			t.Errorf("read-only SRT metadata key %q collides with a persisted field", field.Key)
		}
		if !field.ReadOnly && !srtModelKeys[field.Key] {
			t.Errorf("persisted SRT metadata key %q has no model field", field.Key)
		}
	}
	for _, field := range WebRTCMetadata() {
		if field.ReadOnly && webrtcModelKeys[field.Key] && !fixedModelFields[field.Key] {
			t.Errorf("read-only WebRTC metadata key %q collides with a persisted field", field.Key)
		}
		if !field.ReadOnly && !webrtcModelKeys[field.Key] {
			t.Errorf("persisted WebRTC metadata key %q has no model field", field.Key)
		}
	}
	for fixedKey := range fixedModelFields {
		field, ok := FieldByKey(WebRTCMetadata(), fixedKey)
		if !ok {
			t.Errorf("fixed model field %q has no metadata entry", fixedKey)
			continue
		}
		if !field.ReadOnly || field.ApplyMode != ApplyNone {
			t.Errorf("fixed model field %q must be read-only metadata, got %+v", fixedKey, field)
		}
	}
}

// structValueByJSONTag reads a struct field value by its JSON tag.
func structValueByJSONTag(t *testing.T, value any, key string) reflect.Value {
	t.Helper()
	structValue := reflect.ValueOf(value)
	typ := structValue.Type()
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		tagKey, _, _ := strings.Cut(tag, ",")
		if tagKey == key {
			return structValue.Field(i)
		}
	}
	t.Fatalf("model has no field with json tag %q", key)
	return reflect.Value{}
}

func TestSRTMetadataDefaultsMatchModelDefaults(t *testing.T) {
	defaults := models.DefaultSRTConfig()
	for _, field := range SRTMetadata() {
		if field.ReadOnly {
			continue
		}
		modelValue := structValueByJSONTag(t, defaults, field.Key).Interface()
		if !reflect.DeepEqual(field.Default, modelValue) {
			t.Errorf("SRT metadata default for %q does not match models.DefaultSRTConfig(): metadata=%#v model=%#v", field.Key, field.Default, modelValue)
		}
	}
}

func TestWebRTCMetadataDefaultsMatchModelDefaults(t *testing.T) {
	defaults := models.DefaultWebRTCConfig()
	for _, field := range WebRTCMetadata() {
		if field.ReadOnly && !fixedModelFields[field.Key] {
			continue
		}
		modelValue := structValueByJSONTag(t, defaults, field.Key).Interface()
		if !reflect.DeepEqual(field.Default, modelValue) {
			t.Errorf("WebRTC metadata default for %q does not match models.DefaultWebRTCConfig(): metadata=%#v model=%#v", field.Key, field.Default, modelValue)
		}
	}
}

func TestMetadataTypesMatchModelKinds(t *testing.T) {
	srt := models.DefaultSRTConfig()
	for _, field := range SRTMetadata() {
		if field.ReadOnly {
			continue
		}
		kind := structValueByJSONTag(t, srt, field.Key).Kind()
		assertTypeMatchesKind(t, "srt", field, kind)
	}
	webrtc := models.DefaultWebRTCConfig()
	for _, field := range WebRTCMetadata() {
		if field.ReadOnly {
			continue
		}
		kind := structValueByJSONTag(t, webrtc, field.Key).Kind()
		assertTypeMatchesKind(t, "webrtc", field, kind)
	}
}

func assertTypeMatchesKind(t *testing.T, section string, field Field, kind reflect.Kind) {
	t.Helper()
	switch kind {
	case reflect.Bool:
		if field.Type != TypeBool {
			t.Errorf("%s.%s: bool model field has metadata type %q", section, field.Key, field.Type)
		}
	case reflect.Int:
		if field.Type != TypeInt {
			t.Errorf("%s.%s: int model field has metadata type %q", section, field.Key, field.Type)
		}
	case reflect.Float64:
		if field.Type != TypeFloat {
			t.Errorf("%s.%s: float model field has metadata type %q", section, field.Key, field.Type)
		}
	case reflect.String:
		switch field.Type {
		case TypeString, TypeEnum, TypeSecret:
		default:
			t.Errorf("%s.%s: string model field has metadata type %q", section, field.Key, field.Type)
		}
	case reflect.Slice:
		switch field.Type {
		case TypeStringList, TypeCIDRList, TypeTurnServerList:
		default:
			t.Errorf("%s.%s: slice model field has metadata type %q", section, field.Key, field.Type)
		}
	default:
		t.Errorf("%s.%s: unhandled model kind %v", section, field.Key, kind)
	}
}

func TestEnumDefaultsAreMembers(t *testing.T) {
	for _, fields := range [][]Field{SRTMetadata(), WebRTCMetadata()} {
		for _, field := range fields {
			if len(field.Enum) == 0 {
				continue
			}
			switch value := field.Default.(type) {
			case string:
				if !slices.Contains(field.Enum, value) {
					t.Errorf("%q enum default %q is not in the enum", field.Key, value)
				}
			case int:
				if !slices.Contains(field.Enum, strconv.Itoa(value)) {
					t.Errorf("%q int-enum default %d is not in the enum", field.Key, value)
				}
			case []string:
				for _, entry := range value {
					if !slices.Contains(field.Enum, entry) {
						t.Errorf("%q list default value %q is not in the enum", field.Key, entry)
					}
				}
			default:
				if field.Default != nil {
					t.Errorf("%q has enum %v but default of unsupported type %T", field.Key, field.Enum, field.Default)
				}
			}
		}
	}
}

func TestApplyModesAreValid(t *testing.T) {
	allowed := map[ApplyMode]bool{
		ApplyLive:           true,
		ApplyRestartService: true,
		ApplyRestartStream:  true,
		ApplyRestartServer:  true,
	}
	sections := map[string][]Field{
		"srt":        SRTMetadata(),
		"webrtc":     WebRTCMetadata(),
		"appearance": AppearanceMetadata(),
		"output":     OutputMetadata(),
	}
	for name, fields := range sections {
		for _, field := range fields {
			if field.ReadOnly {
				if field.ApplyMode != ApplyNone {
					t.Errorf("%s.%s: read-only field apply mode = %q", name, field.Key, field.ApplyMode)
				}
				continue
			}
			if !allowed[field.ApplyMode] {
				t.Errorf("%s.%s: invalid apply mode %q", name, field.Key, field.ApplyMode)
			}
		}
	}
}

func TestSecretFieldsAreMarked(t *testing.T) {
	srtField, ok := FieldByKey(SRTMetadata(), "passphrase")
	if !ok {
		t.Fatal("missing passphrase metadata")
	}
	if !srtField.Secret || srtField.Type != TypeSecret {
		t.Errorf("passphrase must be a write-only secret: %+v", srtField)
	}
	if srtField.Default != "" {
		t.Errorf("passphrase default must be empty, got %#v", srtField.Default)
	}

	turnField, ok := FieldByKey(WebRTCMetadata(), "turnServers")
	if !ok {
		t.Fatal("missing turnServers metadata")
	}
	if !turnField.Secret {
		t.Error("turnServers must be marked secret because entries carry credentials")
	}
	if turnField.Type != TypeTurnServerList {
		t.Errorf("turnServers type = %q, want %q", turnField.Type, TypeTurnServerList)
	}
}

func TestGroupsAreReferencedAndDefined(t *testing.T) {
	defined := map[string]bool{}
	for _, group := range Groups() {
		if defined[group.ID] {
			t.Errorf("duplicate group id %q", group.ID)
		}
		defined[group.ID] = true
	}
	used := map[string]bool{}
	sections := map[string][]Field{
		"srt":        SRTMetadata(),
		"webrtc":     WebRTCMetadata(),
		"appearance": AppearanceMetadata(),
		"output":     OutputMetadata(),
	}
	for name, fields := range sections {
		for _, field := range fields {
			used[field.Group] = true
			if !defined[field.Group] {
				t.Errorf("%s.%s references undefined group %q", name, field.Key, field.Group)
			}
		}
	}
	for group := range defined {
		if !used[group] {
			t.Errorf("group %q is defined but no field uses it", group)
		}
	}
}

func TestMetadataJSONShape(t *testing.T) {
	payload, err := json.Marshal(All())
	if err != nil {
		t.Fatalf("marshaling metadata: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshaling metadata: %v", err)
	}

	topLevel := []string{"groups", "srt", "webrtc", "appearance", "output"}
	for _, key := range topLevel {
		if _, ok := decoded[key]; !ok {
			t.Errorf("metadata JSON is missing top-level key %q", key)
		}
	}
	if len(decoded) != len(topLevel) {
		t.Errorf("metadata JSON has unexpected top-level keys: %v", decoded)
	}

	wantFieldKeys := []string{"key", "label", "group", "type", "default", "min", "max", "enum", "unit", "description", "secret", "applyMode", "readOnly"}
	slices.Sort(wantFieldKeys)
	for _, section := range []string{"srt", "webrtc", "appearance", "output"} {
		sectionObject, ok := decoded[section].(map[string]any)
		if !ok {
			t.Fatalf("metadata section %q is not an object", section)
		}
		fields, ok := sectionObject["fields"].([]any)
		if !ok || len(fields) == 0 {
			t.Fatalf("metadata section %q has no fields", section)
		}
		for index, rawField := range fields {
			field, ok := rawField.(map[string]any)
			if !ok {
				t.Fatalf("%s field %d is not an object", section, index)
			}
			actualKeys := make([]string, 0, len(field))
			for key := range field {
				actualKeys = append(actualKeys, key)
			}
			slices.Sort(actualKeys)
			if !slices.Equal(actualKeys, wantFieldKeys) {
				t.Errorf("%s field %d JSON keys = %v, want %v", section, index, actualKeys, wantFieldKeys)
			}
		}
	}
}

func TestEveryFieldHasLabel(t *testing.T) {
	sections := map[string][]Field{
		"srt":        SRTMetadata(),
		"webrtc":     WebRTCMetadata(),
		"appearance": AppearanceMetadata(),
		"output":     OutputMetadata(),
	}
	for name, fields := range sections {
		for _, field := range fields {
			if field.Label == "" {
				t.Errorf("%s.%s has no label", name, field.Key)
			}
		}
	}
}

func TestFieldByKey(t *testing.T) {
	if _, ok := FieldByKey(SRTMetadata(), "port"); !ok {
		t.Error("expected to find the SRT port field")
	}
	if _, ok := FieldByKey(SRTMetadata(), "does-not-exist"); ok {
		t.Error("unexpected match for an unknown key")
	}
}

func TestEffectiveFieldsExcludeReadOnly(t *testing.T) {
	for _, field := range EffectiveSRTFields() {
		if field.ReadOnly {
			t.Errorf("effective SRT field %q is read-only", field.Key)
		}
	}
	for _, field := range EffectiveWebRTCFields() {
		if field.ReadOnly {
			t.Errorf("effective WebRTC field %q is read-only", field.Key)
		}
	}
}

func TestSRTNotSupportedOptionsPopulated(t *testing.T) {
	options := SRTNotSupportedOptions()
	if len(options) < 20 {
		t.Fatalf("expected at least 20 not-supported entries, got %d", len(options))
	}
	for _, option := range options {
		if option.Capability == "" || option.Status == "" || option.Handling == "" || option.Explanation == "" {
			t.Errorf("incomplete not-supported entry: %+v", option)
		}
	}
}

func TestOutputProcessModelMetadata(t *testing.T) {
	field, ok := FieldByKey(OutputMetadata(), "processModel")
	if !ok {
		t.Fatal("missing processModel metadata")
	}
	if field.Default != models.OutputProcessModelShared {
		t.Errorf("processModel default = %v, want %q", field.Default, models.OutputProcessModelShared)
	}
	if !slices.Contains(field.Enum, models.OutputProcessModelIsolated) {
		t.Error("processModel enum is missing the isolated model")
	}
	if !IsValidOutputProcessModel(models.OutputProcessModelShared) || !IsValidOutputProcessModel(models.OutputProcessModelIsolated) {
		t.Error("known process models reported as invalid")
	}
	if IsValidOutputProcessModel("unknown") {
		t.Error("unknown process model reported as valid")
	}
}
