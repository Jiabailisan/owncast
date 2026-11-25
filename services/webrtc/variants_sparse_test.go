package webrtc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/owncast/owncast/models"
)

func TestServiceVariantsSparseIndices(t *testing.T) {
	cfg := models.DefaultWebRTCConfig()
	cfg.Enabled = true
	svc := New(Deps{Config: cfg})

	// Variant 0 is HLS-only; variant 1 and variant 3 are WebRTC-enabled.
	variants := []models.StreamOutputVariant{
		{
			Name:         "HLS Only",
			VideoBitrate: 4000,
			Outputs: &models.VariantOutputs{
				HLS:    &models.ProtocolOutputSettings{Enabled: models.BoolPtr(true)},
				WebRTC: &models.ProtocolOutputSettings{Enabled: models.BoolPtr(false)},
			},
		},
		{
			Name:         "WebRTC 720p",
			VideoBitrate: 2500,
			Outputs: &models.VariantOutputs{
				WebRTC: &models.ProtocolOutputSettings{Enabled: models.BoolPtr(true)},
			},
		},
		{
			Name:         "HLS Only 2",
			VideoBitrate: 1500,
			Outputs: &models.VariantOutputs{
				WebRTC: &models.ProtocolOutputSettings{Enabled: models.BoolPtr(false)},
			},
		},
		{
			Name:         "WebRTC 480p",
			VideoBitrate: 1000,
			Outputs: &models.VariantOutputs{
				WebRTC: &models.ProtocolOutputSettings{Enabled: models.BoolPtr(true)},
			},
		},
	}

	gen, err := svc.SetVariants(variants)
	require.NoError(t, err)

	got := svc.Variants()
	require.Len(t, got, 2)
	assert.Equal(t, 1, got[0].Index)
	assert.Equal(t, "WebRTC 720p", got[0].Name)
	assert.Equal(t, 3, got[1].Index)
	assert.Equal(t, "WebRTC 480p", got[1].Name)

	for _, warning := range svc.Warnings() {
		assert.NotEqual(t, "webrtc_no_variants", warning)
	}

	// Disabling variant 1 must still keep variant 3 visible in Variants().
	svc.DisableVariants(gen, []int{1})
	got = svc.Variants()
	require.Len(t, got, 1)
	assert.Equal(t, 3, got[0].Index)
	assert.Equal(t, "WebRTC 480p", got[0].Name)
}
