package configrepository

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/owncast/owncast/config"
	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/datastore"
	"github.com/owncast/owncast/services/transcoder"
)

// storedVariantsJSON unwraps the datastore gob container and returns the
// repository payload bytes, so tests can assert the on-disk format.
func storedVariantsJSON(t *testing.T, ds *datastore.Datastore) []byte {
	t.Helper()
	entry, err := ds.Get(videoStreamOutputVariantsKey)
	mustNoError(t, err)
	raw, ok := entry.Value.([]byte)
	if !ok {
		t.Fatalf("stored value is %T, want []byte", entry.Value)
	}
	var payload []byte
	if err := gob.NewDecoder(bytes.NewReader(raw)).Decode(&payload); err != nil {
		t.Fatalf("stored value is not a gob-wrapped byte slice: %v", err)
	}
	return payload
}

func TestStreamOutputVariantsRoundTripKeepsTheConfiguredFramerate(t *testing.T) {
	repo, _ := newPhase1TestRepo(t)

	// The effective framerate of a passthrough variant is 0 and of an unset one is
	// 24, both computed by GetFramerate. Persisting must keep what was configured:
	// JSON marshalling used to run MarshalJSON and store the computed value.
	variants := []models.StreamOutputVariant{
		{Name: "passthrough configured as 30", IsVideoPassthrough: true, Framerate: 30, CPUUsageLevel: 0},
		{Name: "unset framerate", VideoBitrate: 900, Framerate: 0, CPUUsageLevel: 2},
	}

	mustNoError(t, repo.SetStreamOutputVariants(variants))

	stored := repo.GetStreamOutputVariants()
	if len(stored) != len(variants) {
		t.Fatalf("got %d variants, want %d", len(stored), len(variants))
	}

	if stored[0].Framerate != 30 {
		t.Errorf("passthrough framerate round-tripped as %d, want 30", stored[0].Framerate)
	}
	if stored[1].Framerate != 0 {
		t.Errorf("unset framerate round-tripped as %d, want 0", stored[1].Framerate)
	}
}

func TestStreamOutputVariantsRoundTripPreservesPointerToFalse(t *testing.T) {
	repo, ds := newPhase1TestRepo(t)

	hlsDisabled := models.BoolPtr(false)
	webRTCEnabled := models.BoolPtr(true)
	variants := []models.StreamOutputVariant{
		{Name: "transcode", VideoBitrate: 1200, AudioBitrate: 128, Framerate: 24, CPUUsageLevel: 2},
		{
			Name: "hls disabled", VideoBitrate: 800, Framerate: 24, CPUUsageLevel: 2,
			Outputs: &models.VariantOutputs{
				HLS: &models.ProtocolOutputSettings{
					Enabled:      hlsDisabled,
					Mode:         models.OutputModeTranscode,
					VideoBitrate: 640,
				},
			},
		},
		{
			Name: "webrtc enabled", VideoBitrate: 600, Framerate: 24, CPUUsageLevel: 2,
			Outputs: &models.VariantOutputs{
				WebRTC: &models.ProtocolOutputSettings{
					Enabled:      webRTCEnabled,
					Mode:         models.OutputModeInherit,
					CopyFallback: models.CopyFallbackDisable,
				},
			},
		},
	}

	mustNoError(t, repo.SetStreamOutputVariants(variants))
	got := repo.GetStreamOutputVariants()

	if !reflect.DeepEqual(got, variants) {
		t.Fatalf("round-trip mismatch:\n got: %#v\nwant: %#v", got, variants)
	}
	if got[1].HLSEnabled() {
		t.Fatal("outputs.hls.enabled=false was lost: variant reports HLS enabled after round-trip")
	}
	if got[2].Outputs == nil || got[2].Outputs.WebRTC == nil || got[2].Outputs.WebRTC.Enabled == nil || !*got[2].Outputs.WebRTC.Enabled {
		t.Fatalf("explicit webrtc enabled=true was lost: %#v", got[2])
	}
	if got[0].Outputs != nil {
		t.Fatalf("nil outputs must stay nil: %#v", got[0].Outputs)
	}

	// New writes are JSON (gob-wrapped by the datastore), not a gob slice.
	payload := storedVariantsJSON(t, ds)
	if !json.Valid(payload) || !strings.HasPrefix(strings.TrimSpace(string(payload)), "[") {
		t.Fatalf("stored payload is not JSON: %q", string(payload))
	}
}

func TestStreamOutputVariantsLegacyGobValueStillReadable(t *testing.T) {
	repo, ds := newPhase1TestRepo(t)

	legacy := []models.StreamOutputVariant{
		{Name: "legacy", VideoBitrate: 1000, AudioBitrate: 96, Framerate: 30, CPUUsageLevel: 1},
	}
	newEncodingLegacyValue(t, ds, legacy)

	// Simulate an existing deployment: a value written by the old
	// SetStreamOutputVariants implementation (gob-encoded slice).
	got := repo.GetStreamOutputVariants()
	if !reflect.DeepEqual(got, legacy) {
		t.Fatalf("legacy gob value not decoded:\n got: %#v\nwant: %#v", got, legacy)
	}

	// The next save upgrades the value to JSON while preserving the explicit
	// pointer-to-false that gob used to flatten.
	hlsDisabled := models.BoolPtr(false)
	upgraded := []models.StreamOutputVariant{{
		Name: "upgraded", VideoBitrate: 900, Framerate: 24, CPUUsageLevel: 2,
		Outputs: &models.VariantOutputs{HLS: &models.ProtocolOutputSettings{Enabled: hlsDisabled}},
	}}
	mustNoError(t, repo.SetStreamOutputVariants(upgraded))
	got = repo.GetStreamOutputVariants()
	if !reflect.DeepEqual(got, upgraded) || got[0].HLSEnabled() {
		t.Fatalf("legacy migration lost values: %#v", got)
	}
}

// newEncodingLegacyValue writes a gob-encoded variants slice through the
// datastore, reproducing the pre-JSON stored format.
func newEncodingLegacyValue(t *testing.T, ds *datastore.Datastore, variants []models.StreamOutputVariant) {
	t.Helper()
	mustNoError(t, ds.Save(models.ConfigEntry{Key: videoStreamOutputVariantsKey, Value: variants}))
}

func TestTranscoderHLSFilterSeesDisabledAfterRepositoryRoundTrip(t *testing.T) {
	repo, _ := newPhase1TestRepo(t)

	hlsDisabled := models.BoolPtr(false)
	variants := []models.StreamOutputVariant{
		{VideoBitrate: 1200, Framerate: 24, IsAudioPassthrough: true, CPUUsageLevel: 2},
		{
			VideoBitrate: 800, Framerate: 24, IsAudioPassthrough: true, CPUUsageLevel: 2,
			Outputs: &models.VariantOutputs{HLS: &models.ProtocolOutputSettings{Enabled: hlsDisabled}},
		},
	}
	mustNoError(t, repo.SetStreamOutputVariants(variants))

	tr := transcoder.NewTranscoder(&config.Config{
		InternalHLSListenerPort: "8927",
		InternalHLSListenerHost: "127.0.0.1",
	}, repo)
	command := tr.GetString()

	if !strings.Contains(command, "v:0,a:0") {
		t.Fatalf("HLS-enabled variant missing from var_stream_map: %s", command)
	}
	if strings.Contains(command, "v:1,a:1") {
		t.Fatalf("HLS-disabled variant was emitted after repository round-trip: %s", command)
	}
}
