package stream

import (
	"testing"

	"github.com/owncast/owncast/models"
)

// A registration that started against one broadcast must not add itself to the next
// one, even with the same client ID.
func TestStaleRegistrationDoesNotTouchTheNextBroadcast(t *testing.T) {
	repo := newStreamStatsTestRepo(t)
	service := &Service{
		configRepository: repo,
		stats:            &models.Stats{StreamConnected: true, Viewers: map[string]*models.Viewer{}},
	}
	service.broadcastEpoch = 1

	snapshot := service.LiveSnapshot()

	// The broadcast switches before the old request registers.
	service.statsMu.Lock()
	service.broadcastEpoch = 2
	service.statsMu.Unlock()

	service.SetViewerActiveForSnapshot(newHLSViewer("stale"), snapshot)

	service.statsMu.Lock()
	_, exists := service.stats.Viewers["stale"]
	service.statsMu.Unlock()
	if exists {
		t.Fatal("a stale registration was added to the next broadcast")
	}

	// The same client registering against the current broadcast still works.
	current := service.LiveSnapshot()
	service.SetViewerActiveForSnapshot(newHLSViewer("stale"), current)

	service.statsMu.Lock()
	_, exists = service.stats.Viewers["stale"]
	service.statsMu.Unlock()
	if !exists {
		t.Fatal("a registration for the current broadcast must be recorded")
	}
}

// A viewer registered against the live broadcast keeps the quality it reported.
func TestRegistrationForTheCurrentBroadcastKeepsQuality(t *testing.T) {
	repo := newStreamStatsTestRepo(t)
	service := &Service{
		configRepository: repo,
		stats:            &models.Stats{StreamConnected: true, Viewers: map[string]*models.Viewer{}},
	}
	service.broadcastEpoch = 1

	service.SetViewerActiveWithVariantForSnapshot(newHLSViewer("viewer-1"), 0, service.LiveSnapshot())

	service.statsMu.Lock()
	stored := service.stats.Viewers["viewer-1"]
	service.statsMu.Unlock()

	if stored == nil {
		t.Fatal("the viewer was not registered")
	}
	if stored.VariantIndex == nil || *stored.VariantIndex != 0 {
		t.Fatalf("variant index = %v, want 0", stored.VariantIndex)
	}
}
