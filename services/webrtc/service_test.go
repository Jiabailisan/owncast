package webrtc

import (
	"context"
	"testing"
	"time"

	"github.com/owncast/owncast/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceStartStopLifecycle(t *testing.T) {
	svc := New(Deps{Config: testWebRTCConfig(nil)})
	require.NoError(t, svc.Start(context.Background()))
	assert.True(t, svc.IsStarted())
	require.NoError(t, svc.Stop(context.Background()))
	assert.False(t, svc.IsStarted())

	// Start/Stop are idempotent and restartable.
	require.NoError(t, svc.Stop(context.Background()))
	require.NoError(t, svc.Start(context.Background()))
	require.NoError(t, svc.Stop(context.Background()))
}

func TestServiceUpdateConfigRestartRequired(t *testing.T) {
	svc := New(Deps{Config: testWebRTCConfig(nil)})
	require.NoError(t, svc.Start(context.Background()))
	t.Cleanup(func() { require.NoError(t, svc.Stop(context.Background())) })

	// Live settings apply immediately.
	updated := svc.Config()
	updated.MaxViewers = 5
	updated.StrictDomain = true
	pending, err := svc.UpdateConfig(updated)
	require.NoError(t, err)
	assert.Empty(t, pending)

	// Restart-service settings are reported by key.
	updated.UDPPort = 9191
	updated.NACK = false
	pending, err = svc.UpdateConfig(updated)
	require.NoError(t, err)
	assert.Equal(t, []string{"nack", "udpPort"}, pending)
	assert.False(t, svc.Config().NACK)
}

func TestServiceUpdateConfigValidation(t *testing.T) {
	svc := New(Deps{Config: testWebRTCConfig(nil)})
	require.NoError(t, svc.Start(context.Background()))
	t.Cleanup(func() { require.NoError(t, svc.Stop(context.Background())) })

	invalid := svc.Config()
	invalid.UDPMode = "bogus"
	_, err := svc.UpdateConfig(invalid)
	require.ErrorIs(t, err, ErrInvalidConfig)

	embeddedTurn := svc.Config()
	embeddedTurn.EmbeddedTurn = true
	_, err = svc.UpdateConfig(embeddedTurn)
	require.ErrorIs(t, err, ErrEmbeddedTurnDisabled)
}

func TestServiceSetVariantsReusesUnchangedTracks(t *testing.T) {
	svc := New(Deps{Config: testWebRTCConfig(nil)})
	require.NoError(t, svc.Start(context.Background()))
	t.Cleanup(func() { require.NoError(t, svc.Stop(context.Background())) })

	variants := testWebRTCVariants(2)
	require.NoError(t, svc.SetVariants(variants))
	first, ok := svc.variantFor(0)
	require.True(t, ok)
	require.Len(t, svc.Variants(), 2)

	require.NoError(t, svc.SetVariants(variants))
	second, ok := svc.variantFor(0)
	require.True(t, ok)
	assert.Same(t, first, second, "unchanged variant must reuse its shared tracks")

	// Dropping a variant removes its tracks.
	require.NoError(t, svc.SetVariants(variants[:1]))
	_, ok = svc.variantFor(1)
	assert.False(t, ok)
}

func TestServiceSetVariantsInvalidProfile(t *testing.T) {
	// The profile is validated both at Start and when variants are built; an
	// invalid profile must not panic or create tracks.
	svc := New(Deps{Config: testWebRTCConfig(func(cfg *models.WebRTCConfig) { cfg.H264Profile = "bogus" })})
	require.ErrorIs(t, svc.SetVariants(testWebRTCVariants(1)), ErrInvalidConfig)
	require.ErrorIs(t, svc.Start(context.Background()), ErrInvalidConfig)
}

func TestServiceStartRejectsEmbeddedTurn(t *testing.T) {
	svc := New(Deps{Config: testWebRTCConfig(func(cfg *models.WebRTCConfig) { cfg.EmbeddedTurn = true })})
	require.ErrorIs(t, svc.Start(context.Background()), ErrEmbeddedTurnDisabled)
}

func TestServiceDisabledHasNoEngine(t *testing.T) {
	svc := New(Deps{Config: testWebRTCConfig(func(cfg *models.WebRTCConfig) { cfg.Enabled = false })})
	require.NoError(t, svc.Start(context.Background()))
	t.Cleanup(func() { require.NoError(t, svc.Stop(context.Background())) })

	assert.Nil(t, svc.engineInstance())
	require.NoError(t, svc.SetVariants(testWebRTCVariants(1)))
	assert.Len(t, svc.Variants(), 1)
}

func TestServiceReservationExpiryDoesNotLeakCapacity(t *testing.T) {
	now := time.Unix(1000, 0)
	gate := newCapacityGate(1, 0, 10*time.Second, func() time.Time { return now })
	_, err := gate.reserve("203.0.113.1")
	require.NoError(t, err)
	now = now.Add(11 * time.Second)
	gate.expire(now)
	assert.Zero(t, gate.snapshot().Reserved)
}
