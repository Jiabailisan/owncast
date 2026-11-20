package webrtc

import (
	"context"
	"testing"
	"time"

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

// newVariantTestService builds the smallest service that can answer variant and
// session questions without an engine.
func newVariantTestService(t *testing.T) (*Service, *variantTracks, *variantTracks) {
	t.Helper()

	const fmtp = "profile-level-id=42e01f;packetization-mode=1"

	tracks0, err := newVariantTracks(Variant{Index: 0, Name: "v0", Width: 1280, Height: 720, FPS: 30}, fmtp)
	require.NoError(t, err)
	tracks1, err := newVariantTracks(Variant{Index: 1, Name: "v1", Width: 1920, Height: 1080, FPS: 30}, fmtp)
	require.NoError(t, err)

	svc := &Service{
		sessions: newSessionRegistry(),
		variants: map[int]*variantTracks{0: tracks0, 1: tracks1},
		capacity: newCapacityGate(10, 5, time.Minute, nil),
	}

	return svc, tracks0, tracks1
}

// A variant withdrawn while a session is still negotiating must not be published:
// the viewer gets the variant-not-found error instead of a session on tracks nobody
// serves, and no capacity is consumed.
func TestRegisterSessionRefusesAWithdrawnVariant(t *testing.T) {
	svc, tracks0, _ := newVariantTestService(t)
	sess := newSession("s1", tracks0, nil, nil, nil, "127.0.0.1", "ua", nil, nil)

	// The withdrawal lands between negotiation and registration.
	svc.DisableVariants([]int{0})

	assert.False(t, svc.registerSession(sess), "a withdrawn variant must not be published")
	assert.Empty(t, svc.sessions.all(), "the refused session must not be registered")

	svc.capacity.mu.Lock()
	active, holding := len(svc.capacity.active), len(svc.capacity.holding)
	svc.capacity.mu.Unlock()
	assert.Zero(t, active, "the refused session must not hold an active slot")
	assert.Zero(t, holding, "the refused session must not leave a reservation")

	first, ok := svc.firstVariant()
	assert.True(t, ok, "the remaining variant must stay selectable")
	assert.Equal(t, 1, first, "the default selection must fall through to the ready variant")
}

// The opposite interleaving: the session is registered first and the variant is
// withdrawn afterwards. The withdrawal has to drop the session rather than leave it
// reachable on withdrawn tracks.
func TestDisableVariantsDropsAnAlreadyRegisteredSession(t *testing.T) {
	svc, tracks0, _ := newVariantTestService(t)
	sess := newSession("s2", tracks0, nil, nil, nil, "127.0.0.1", "ua", nil, nil)

	require.True(t, svc.registerSession(sess), "the ready variant must accept a session")
	require.Len(t, svc.sessions.all(), 1)

	svc.DisableVariants([]int{0})

	assert.Empty(t, svc.sessions.all(), "the session on the withdrawn variant must be dropped")
}
