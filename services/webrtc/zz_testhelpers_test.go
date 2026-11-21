package webrtc

import (
	"testing"

	"github.com/owncast/owncast/models"
)

// setVariants publishes a variant set and returns only its error, for tests that do
// not care which generation the publication created.
func setVariants(svc *Service, variants []models.StreamOutputVariant) error {
	_, err := svc.SetVariants(variants)

	return err
}

// markDeliverable gives the variant a media leg bound to its published tracks, which
// is exactly the invariant OpenMediaLeg + publishLeg establish. Tests use it instead
// of binding real sockets; it goes through the same fields production checks.
func markDeliverable(svc *Service, index int) bool {
	svc.mu.Lock()
	defer svc.mu.Unlock()

	tracks, ok := svc.variants[index]
	if !ok {
		return false
	}
	if svc.legs == nil {
		svc.legs = map[int]*MediaLeg{}
	}
	svc.legs[index] = &MediaLeg{service: svc, variant: index, tracks: tracks, generation: svc.variantGeneration}

	return true
}

// setDeliverableVariants publishes a variant set and gives every variant a leg bound
// to its tracks, so handler tests reach the admit path the way production does.
func setDeliverableVariants(t *testing.T, svc *Service, variants []models.StreamOutputVariant) {
	t.Helper()

	if err := setVariants(svc, variants); err != nil {
		t.Fatalf("publish variants: %v", err)
	}
	for index := range variants {
		if !markDeliverable(svc, index) {
			t.Fatalf("variant %d was not published", index)
		}
	}
}
