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

	svc.DisableVariants(svc.VariantGeneration(), []int{1})

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
		legs:     map[int]*MediaLeg{},
	}
	// Both variants start deliverable; the tests withdraw them explicitly.
	markDeliverable(svc, 0)
	markDeliverable(svc, 1)

	return svc, tracks0, tracks1
}

// A variant withdrawn while a session is still negotiating must not be published:
// the viewer gets the variant-not-found error instead of a session on tracks nobody
// serves, and no capacity is consumed.
func TestRegisterSessionRefusesAWithdrawnVariant(t *testing.T) {
	svc, tracks0, _ := newVariantTestService(t)
	sess := newSession("s1", tracks0, nil, nil, nil, "127.0.0.1", "ua", nil, nil)

	reservation, err := svc.capacity.reserve("127.0.0.1")
	require.NoError(t, err)

	// The withdrawal lands between negotiation and registration.
	svc.DisableVariants(svc.VariantGeneration(), []int{0})

	err = svc.registerSessionWithCapacity(sess, reservation)
	require.ErrorIs(t, err, ErrVariantNotFound, "a withdrawn variant must not be published")
	assert.Empty(t, svc.sessions.all(), "the refused session must not be registered")

	svc.capacity.mu.Lock()
	active, holding := len(svc.capacity.active), len(svc.capacity.holding)
	svc.capacity.mu.Unlock()
	assert.Zero(t, active, "the refused session must not hold an active slot")
	assert.Equal(t, 1, holding, "the reservation must still be held for the caller to release")

	// The caller's deferred release is what frees it.
	svc.capacity.releaseReservation(reservation)
	svc.capacity.mu.Lock()
	holding = len(svc.capacity.holding)
	svc.capacity.mu.Unlock()
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

	reservation, err := svc.capacity.reserve("127.0.0.1")
	require.NoError(t, err)
	require.NoError(t, svc.registerSessionWithCapacity(sess, reservation), "the ready variant must accept a session")
	require.Len(t, svc.sessions.all(), 1)

	svc.capacity.mu.Lock()
	active := len(svc.capacity.active)
	svc.capacity.mu.Unlock()
	assert.Equal(t, 1, active, "the registered session must hold its slot")

	svc.DisableVariants(svc.VariantGeneration(), []int{0})

	assert.Empty(t, svc.sessions.all(), "the session on the withdrawn variant must be dropped")

	svc.capacity.mu.Lock()
	active = len(svc.capacity.active)
	svc.capacity.mu.Unlock()
	assert.Zero(t, active, "dropping the session must free its slot")
}

// A partial leg failure withdraws only the affected variant: the others stay
// selectable, the indices do not move, and the default falls through to the lowest
// one that is still available.
func TestDisableVariantsWithdrawsOnlyTheAffectedVariant(t *testing.T) {
	svc, _, _ := newVariantTestService(t)
	generation := svc.VariantGeneration()

	svc.DisableVariants(generation, []int{0})

	_, ok := svc.variantFor(0)
	assert.False(t, ok, "the failed variant must not be offered")
	_, ok = svc.variantFor(1)
	assert.True(t, ok, "the variant that bound must stay available")

	first, ok := svc.firstVariant()
	assert.True(t, ok)
	assert.Equal(t, 1, first, "the default must fall through to the available variant")

	// Publishing again keeps the index of the surviving variant.
	svc.DisableVariants(svc.VariantGeneration(), []int{1})
	_, ok = svc.firstVariant()
	assert.False(t, ok, "no variant is left, so the default selection has no candidate")
}

// Cleanup from a finished stream must not withdraw the variants the current stream
// published at the same indices.
func TestDisableVariantsIgnoresAStaleGeneration(t *testing.T) {
	svc, _, _ := newVariantTestService(t)
	stale := svc.VariantGeneration()

	// A new stream publishes its own variants, which bumps the generation.
	svc.mu.Lock()
	svc.variantGeneration++
	svc.mu.Unlock()

	svc.DisableVariants(stale, []int{0})

	if _, ok := svc.variantFor(0); !ok {
		t.Fatal("a stale generation withdrew the current stream's variant")
	}
}

// A leg that finishes opening after the publication it was created for has been
// replaced must not be adopted: publishLeg checks the tracks it feeds under the lock
// that replaced them.
func TestLateOpenMediaLegIsNotAdoptedAfterRepublish(t *testing.T) {
	svc, tracks0, _ := newVariantTestService(t)

	// A leg opened for an older publication of index 0.
	stale := &MediaLeg{service: svc, variant: 0, tracks: tracks0}

	// The current publication of index 0 uses different tracks.
	replacement, err := newVariantTracks(Variant{Index: 0, Name: "v0b", Width: 640, Height: 360, FPS: 30},
		"profile-level-id=42e01f;packetization-mode=1")
	require.NoError(t, err)
	svc.mu.Lock()
	svc.variants[0] = replacement
	svc.variantGeneration++
	svc.mu.Unlock()

	if svc.publishLeg(0, stale) {
		t.Fatal("a leg for a replaced publication was adopted")
	}

	if _, ok := svc.deliverableVariant(0); ok {
		t.Fatal("the stale leg made the variant deliverable")
	}
}
