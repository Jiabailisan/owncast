package handlers

import (
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
}
