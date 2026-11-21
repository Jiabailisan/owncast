package stream

import (
	"errors"
	"testing"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/ingest"
)

// A session that lost the publisher slot must not publish: no active session and no
// online announcement, so no transcoder starts for it.
func TestHandleIngestSessionRefusesASessionThatLostTheSlot(t *testing.T) {
	repo := newStreamStatsTestRepo(t)
	arbiter := ingest.NewArbiter()
	svc := &Service{
		configRepository: repo,
		sessions:         arbiter,
		stats:            &models.Stats{Viewers: map[string]*models.Viewer{}},
	}

	winner := newTestIngestSession("winner")
	if err := arbiter.TryAcquire(winner); err != nil {
		t.Fatalf("acquire winner: %v", err)
	}

	loser := newTestIngestSession("loser")
	svc.HandleIngestSession(loser)

	if svc.currentSession != nil {
		t.Fatal("a session that lost the slot must not be published")
	}
	if svc.stats.StreamConnected {
		t.Fatal("a session that lost the slot must not announce the stream online")
	}
}

// A completion reported by a stream that already lost the broadcast must not tear
// down the new stream's state.
func TestCompletionFromAReplacedSessionKeepsTheNewStream(t *testing.T) {
	arbiter := ingest.NewArbiter()
	svc := &Service{
		sessions: arbiter,
		stats:    &models.Stats{StreamConnected: true, Viewers: map[string]*models.Viewer{}},
	}

	oldSession := newTestIngestSession("old")
	if err := arbiter.TryAcquire(oldSession); err != nil {
		t.Fatalf("acquire old: %v", err)
	}
	if !svc.setCurrentSession(oldSession) {
		t.Fatal("the old session must be published")
	}

	// The new publisher takes the slot and the broadcast.
	newSession := newTestIngestSession("new")
	arbiter.Release(oldSession)
	if err := arbiter.TryAcquire(newSession); err != nil {
		t.Fatalf("acquire new: %v", err)
	}
	if !svc.setCurrentSession(newSession) {
		t.Fatal("the new session must be published")
	}

	// The old FFmpeg now reports completion.
	svc.streamDisconnectedFor(oldSession, errors.New("ffmpeg exited"))

	if svc.currentSession != newSession {
		t.Fatal("a stale completion cleared the new session")
	}
	if !svc.stats.StreamConnected {
		t.Fatal("a stale completion ended the new broadcast")
	}
}
