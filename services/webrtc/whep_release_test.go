package webrtc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A failed negotiation must not leave the engine's per-viewer stats getter and
// bandwidth estimator behind: both are keyed by the PeerConnection, so a closed
// connection that is never released keeps its entry for the life of the process.
func TestNegotiateReleasesTheConnectionWhenTheOfferIsBad(t *testing.T) {
	eng, err := newEngine(context.Background(), testEngineConfig())
	require.NoError(t, err)
	t.Cleanup(eng.close)

	tracks, err := newVariantTracks(
		Variant{Index: 0, Name: "v0", Width: 1280, Height: 720, FPS: 30},
		"profile-level-id=42e01f;packetization-mode=1",
	)
	require.NoError(t, err)

	svc := &Service{}

	_, err = svc.negotiate(eng, tracks, prunedOffer{sdp: "v=0"})
	require.Error(t, err)

	eng.mu.Lock()
	defer eng.mu.Unlock()

	assert.Empty(t, eng.statsGetters, "the stats getter must be released")
	assert.Empty(t, eng.estimators, "the bandwidth estimator must be released")
}

// A variant whose media leg never opened must not stay selectable over WHEP, while
// the variants that did open keep their indices.
func TestDisableVariantsWithdrawsUnreadyVariants(t *testing.T) {
	const fmtp = "profile-level-id=42e01f;packetization-mode=1"

	tracks0, err := newVariantTracks(Variant{Index: 0, Name: "v0", Width: 1280, Height: 720, FPS: 30}, fmtp)
	require.NoError(t, err)
	tracks1, err := newVariantTracks(Variant{Index: 1, Name: "v1", Width: 1920, Height: 1080, FPS: 30}, fmtp)
	require.NoError(t, err)

	svc := &Service{
		sessions: newSessionRegistry(),
		variants: map[int]*variantTracks{0: tracks0, 1: tracks1},
	}

	svc.DisableVariants([]int{1})

	_, ok := svc.variantFor(1)
	assert.False(t, ok, "a variant whose leg failed must not be offered over WHEP")

	_, ok = svc.variantFor(0)
	assert.True(t, ok, "the variant whose leg opened stays available")
}
