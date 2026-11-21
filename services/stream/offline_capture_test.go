package stream

import (
	"testing"

	"github.com/owncast/owncast/models"
)

// The teardown needs the settings the stream started with, while every other reader
// must see the stream as offline immediately. Capturing and clearing in one critical
// section is what makes both true; reading the field after clearing it left the
// cleanup with nothing to write the per-variant offline content from.
func TestOfflineCaptureKeepsTheSettingsForCleanup(t *testing.T) {
	svc := &Service{stats: &models.Stats{StreamConnected: true, Viewers: map[string]*models.Viewer{}}}
	broadcast := &models.CurrentBroadcast{
		OutputSettings: []models.StreamOutputVariant{{Name: "first"}, {Name: "second"}},
	}
	svc.currentBroadcast = broadcast

	captured := svc.beginOffline()

	if captured != broadcast {
		t.Fatal("the cleanup must keep the settings this stream started with")
	}
	if got := len(captured.OutputSettings); got != 2 {
		t.Fatalf("captured %d output settings, want 2", got)
	}

	if svc.currentBroadcast != nil {
		t.Fatal("the shared snapshot must be cleared for other readers")
	}
	if svc.stats.StreamConnected {
		t.Fatal("the stream must read as offline for other readers")
	}
}

// With no settings at all the cleanup keeps its existing fallback.
func TestOfflineCaptureWithoutBroadcast(t *testing.T) {
	svc := &Service{stats: &models.Stats{StreamConnected: true, Viewers: map[string]*models.Viewer{}}}

	if captured := svc.beginOffline(); captured != nil {
		t.Fatal("no settings must stay no settings")
	}
	if svc.stats.StreamConnected {
		t.Fatal("the stream must still read as offline")
	}
}
