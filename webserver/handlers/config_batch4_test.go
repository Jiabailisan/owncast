package handlers

import (
	"encoding/json"
	"testing"

	"github.com/owncast/owncast/models"
)

func TestBuildPublicWebRTCConfigFiltersDisabledLegsAndSetsIsPassthrough(t *testing.T) {
	cfg := models.DefaultWebRTCConfig()
	cfg.Enabled = true
	cfg.FallbackToHLS = false

	broadcast := &models.CurrentBroadcast{
		OutputSettings: []models.StreamOutputVariant{
			{Name: "Source Copy", IsVideoPassthrough: true},
			{Name: "Fallback Disabled", IsVideoPassthrough: true},
			{Name: "720p Transcode", ScaledWidth: 1280, ScaledHeight: 720, Framerate: 30},
		},
	}
	legs := []models.WebRTCLegStatus{
		{VariantIndex: 0, Mode: models.WebRTCLegModeCopy},
		{VariantIndex: 1, Mode: models.WebRTCLegModeDisabled},
		{VariantIndex: 2, Mode: models.WebRTCLegModeTranscode, ScaledWidth: 1280, ScaledHeight: 720, Framerate: 30},
	}

	resp := buildPublicWebRTCConfig(cfg, broadcast, legs)
	if resp == nil {
		t.Fatal("expected non-nil WebRTC config")
	}
	if resp.FallbackToHLS {
		t.Fatal("FallbackToHLS = true, want false")
	}
	if len(resp.Variants) != 2 {
		t.Fatalf("len(Variants) = %d, want 2 (disabled leg excluded): %+v", len(resp.Variants), resp.Variants)
	}
	if resp.Variants[0].Index != 0 || !resp.Variants[0].IsPassthrough {
		t.Fatalf("variant 0 = %+v, want index 0 with IsPassthrough=true", resp.Variants[0])
	}
	if resp.Variants[1].Index != 2 || resp.Variants[1].IsPassthrough {
		t.Fatalf("variant 1 = %+v, want index 2 with IsPassthrough=false", resp.Variants[1])
	}

	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("json.Marshal(resp) failed: %v", err)
	}
	var decoded struct {
		Variants []map[string]any `json:"variants"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	if len(decoded.Variants) != 2 {
		t.Fatalf("decoded variants = %d, want 2", len(decoded.Variants))
	}
	if val, ok := decoded.Variants[0]["isPassthrough"]; !ok || val != true {
		t.Fatalf("variant 0 JSON isPassthrough = %v (present=%v), want true", val, ok)
	}
	if val, ok := decoded.Variants[1]["isPassthrough"]; !ok || val != false {
		t.Fatalf("variant 1 JSON isPassthrough = %v (present=%v), want explicit false", val, ok)
	}
}
