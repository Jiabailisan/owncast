package stream

import (
	"testing"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/ingest"
)

// A metadata callback that arrives after a newer session took over must not touch the
// service-level state.
func TestStaleBroadcasterCallbackIsIgnored(t *testing.T) {
	svc := &Service{stats: &models.Stats{StreamConnected: true, Viewers: map[string]*models.Viewer{}}}
	svc.broadcastEpoch = 2

	oldSession := newTestIngestSession("old")
	current := newTestIngestSession("current")
	svc.sessionMu.Lock()
	svc.currentSession = current
	svc.sessionMu.Unlock()

	svc.BroadcasterSetFromSession(oldSession, models.Broadcaster{
		StreamDetails: models.InboundStreamDetails{VideoBitrate: 999},
	})

	svc.statsMu.Lock()
	stored := svc.broadcaster
	svc.statsMu.Unlock()

	if stored != nil {
		t.Fatalf("a stale callback wrote %+v", stored)
	}
}

// SRT writes only session metadata. HLS quality must still find it, or the viewer record
// has no resolution or bitrate.
func TestSessionMetadataFeedsHLSQuality(t *testing.T) {
	svc := &Service{stats: &models.Stats{StreamConnected: true, Viewers: map[string]*models.Viewer{}}}
	svc.broadcastEpoch = 3

	session := newTestIngestSession("srt")
	details := models.InboundStreamDetails{VideoBitrate: 2500, AudioBitrate: 128, Width: 1280, Height: 720}
	session.SetBroadcaster(&models.Broadcaster{StreamDetails: details})

	svc.sessionMu.Lock()
	svc.currentSession = session
	svc.sessionMu.Unlock()

	// The service-level field stays empty, as it does for SRT.
	got := svc.sourceStreamDetailsFor(3, session)
	if got == nil || got.VideoBitrate != 2500 || got.Width != 1280 {
		t.Fatalf("session details = %+v, want the session's own metadata", got)
	}

	// Without the session the same epoch has no fallback to offer.
	if got := svc.sourceStreamDetailsFor(3, nil); got != nil {
		t.Fatalf("service fallback = %+v, want nothing", got)
	}
}

// The snapshot carries the session the quality is read from.
func TestLiveSnapshotCarriesTheSession(t *testing.T) {
	svc := &Service{stats: &models.Stats{Viewers: map[string]*models.Viewer{}}}
	session := newTestIngestSession("owner")
	// The snapshot session is published with the broadcast, not read from the session lock.
	svc.statsMu.Lock()
	svc.broadcastSession = session
	svc.statsMu.Unlock()

	if got := svc.LiveSnapshot().Session; got != ingest.Session(session) {
		t.Fatalf("snapshot session = %v, want the current session", got)
	}
}
