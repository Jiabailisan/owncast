package admin

import (
	"reflect"
	"testing"

	"github.com/owncast/owncast/models"
)

// TestStreamOutputVariantsForAdminPreservesOutputs guards the admin read path:
// the Video page loads variants from /api/admin/serverconfig and posts the whole
// list back on save. If Outputs is dropped here, per-variant HLS/WebRTC delivery
// settings disappear after a reload and are erased by the next save.
func TestStreamOutputVariantsForAdminPreservesOutputs(t *testing.T) {
	disabled := models.BoolPtr(false)
	enabled := models.BoolPtr(true)
	input := []models.StreamOutputVariant{
		{
			Name:          "720p",
			VideoBitrate:  1200,
			Framerate:     24,
			CPUUsageLevel: 2,
			Outputs: &models.VariantOutputs{
				HLS: &models.ProtocolOutputSettings{Enabled: disabled},
				WebRTC: &models.ProtocolOutputSettings{
					Enabled:      enabled,
					Mode:         models.OutputModeCopy,
					CopyFallback: models.CopyFallbackDisable,
					VideoBitrate: 1500,
				},
			},
		},
		{Name: "legacy", VideoBitrate: 800, Framerate: 30, CPUUsageLevel: 1},
	}

	converted := streamOutputVariantsForAdmin(input)
	if len(converted) != len(input) {
		t.Fatalf("variant count changed: got %d want %d", len(converted), len(input))
	}
	if !reflect.DeepEqual(converted[0].Outputs, input[0].Outputs) {
		t.Fatalf("outputs not preserved: got %#v want %#v", converted[0].Outputs, input[0].Outputs)
	}
	if converted[0].HLSEnabled() {
		t.Fatal("explicit HLS disable must survive the admin conversion")
	}
	if !converted[0].WebRTCEnabled() {
		t.Fatal("explicit WebRTC enable must survive the admin conversion")
	}
	if converted[1].Outputs != nil {
		t.Fatalf("legacy variant must stay without outputs, got %#v", converted[1].Outputs)
	}
	if converted[1].GetName() != "legacy" || converted[1].GetFramerate() != 30 {
		t.Fatalf("legacy defaulting changed: %#v", converted[1])
	}
}
