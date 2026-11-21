package webrtc

import (
	"context"
	"net/http"
	"net/http/httptest"
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
	svc.started = true

	oldGeneration := svc.VariantGeneration()

	// The republished variant reuses the same track set, so only the generation
	// tells the two publications apart.
	svc.mu.Lock()
	svc.variantGeneration++
	currentGeneration := svc.variantGeneration
	svc.mu.Unlock()

	current := &MediaLeg{service: svc, variant: 0, tracks: tracks0, generation: currentGeneration}
	require.True(t, svc.publishLeg(0, current), "the current publication's leg must be adopted")

	stale := &MediaLeg{service: svc, variant: 0, tracks: tracks0, generation: oldGeneration}
	require.False(t, svc.publishLeg(0, stale), "a leg for an older generation must be rejected")

	svc.mu.RLock()
	installed := svc.legs[0]
	svc.mu.RUnlock()
	require.Same(t, current, installed, "the installed leg must not be replaced by the stale one")
}

// Republishing retires only the sessions whose tracks it replaced. A session on a
// reused track set keeps playing, and the capacity count follows.
func TestSetVariantsRetiresOnlyReplacedSessions(t *testing.T) {
	svc := testService(t, nil)
	svc.started = true

	require.NoError(t, setVariants(svc, testWebRTCVariants(2)))
	require.True(t, markDeliverable(svc, 0))
	require.True(t, markDeliverable(svc, 1))

	tracks0, ok := svc.variantFor(0)
	require.True(t, ok)
	tracks1, ok := svc.variantFor(1)
	require.True(t, ok)

	kept := newSession("kept", tracks0, nil, nil, nil, "127.0.0.1", "ua", nil, nil)
	retired := newSession("retired", tracks1, nil, nil, nil, "127.0.0.1", "ua", nil, nil)
	svc.sessions.add(kept)
	svc.sessions.add(retired)

	// The second publication drops index 1 and keeps index 0's tracks.
	require.NoError(t, setVariants(svc, testWebRTCVariants(1)))

	_, keptStill := svc.sessions.get(kept.id)
	_, retiredStill := svc.sessions.get(retired.id)
	assert.True(t, keptStill, "a session on a reused track set must keep playing")
	assert.False(t, retiredStill, "a session on a retired track set must be dropped")
}

// A leg that is closed between negotiation and registration makes the final check
// fail, and no capacity slot is consumed.
func TestRegisterSessionRefusesAVariantWhoseLegWasClosed(t *testing.T) {
	svc, tracks0, _ := newVariantTestService(t)

	reservation, err := svc.capacity.reserve("127.0.0.1")
	require.NoError(t, err)

	// The leg is closed, the tracks stay published.
	svc.mu.Lock()
	delete(svc.legs, 0)
	svc.mu.Unlock()

	sess := newSession("s", tracks0, nil, nil, nil, "127.0.0.1", "ua", nil, nil)
	err = svc.registerSessionWithCapacity(sess, reservation)
	require.ErrorIs(t, err, ErrVariantNotFound)

	assert.Empty(t, svc.sessions.all(), "the refused session must not be registered")

	svc.capacity.mu.Lock()
	active := len(svc.capacity.active)
	svc.capacity.mu.Unlock()
	assert.Zero(t, active, "a refused registration must not consume a slot")
}

// An explicit request for a variant without a leg is refused, while the default
// selection still finds the other ready variant.
func TestExplicitSelectionRefusesAnUnreadyVariant(t *testing.T) {
	svc, _, _ := newVariantTestService(t)

	// Index 0 is published but its RTP leg is not ready.
	svc.mu.Lock()
	delete(svc.legs, 0)
	svc.mu.Unlock()

	handler := &whepHandler{service: svc}

	explicitUnready := httptest.NewRequest(http.MethodPost, "/webrtc/whep?variant=0", nil)
	_, err := handler.selectedVariant(explicitUnready)
	require.Error(t, err, "an explicit request for a variant without a leg must be refused")

	explicitReady := httptest.NewRequest(http.MethodPost, "/webrtc/whep?variant=1", nil)
	index, err := handler.selectedVariant(explicitReady)
	require.NoError(t, err)
	assert.Equal(t, 1, index)

	defaulted := httptest.NewRequest(http.MethodPost, "/webrtc/whep", nil)
	index, err = handler.selectedVariant(defaulted)
	require.NoError(t, err)
	assert.Equal(t, 1, index, "the default selection must skip the unready variant")
}
