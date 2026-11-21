package stream

import (
	"errors"
	"testing"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/ingest"
)

// A session that already ended must not be published: no active session and no
// online announcement. HandleIngestSession returns before any transcoder work.
func TestHandleIngestSessionRefusesAClosedSession(t *testing.T) {
	repo := newStreamStatsTestRepo(t)
	svc := &Service{
		configRepository: repo,
		sessions:         ingest.NewArbiter(),
		stats:            &models.Stats{Viewers: map[string]*models.Viewer{}},
	}

	session := newTestIngestSession("closed")
	session.Close(errors.New("publisher disconnected"))

	svc.HandleIngestSession(session)

	if svc.currentSession != nil {
		t.Fatal("a closed session must not be published as the current stream")
	}
	if svc.stats.StreamConnected {
		t.Fatal("a closed session must not announce the stream online")
	}
}

// Publishing requires the single-publisher slot: a session that lost it is refused,
// the owner is accepted.
func TestSetCurrentSessionRequiresThePublisherSlot(t *testing.T) {
	svc := &Service{sessions: ingest.NewArbiter()}

	owner := newTestIngestSession("owner")
	other := newTestIngestSession("other")
	if err := svc.sessions.TryAcquire(owner); err != nil {
		t.Fatalf("acquire owner: %v", err)
	}

	if svc.setCurrentSession(other) {
		t.Fatal("a session that does not own the slot must not be published")
	}
	if !svc.setCurrentSession(owner) {
		t.Fatal("the slot owner must be published")
	}
}

// A late cleanup from a finished stream must not clear the session a newer stream
// already published and still owns.
func TestStaleOfflineKeepsTheNewSession(t *testing.T) {
	svc := &Service{sessions: ingest.NewArbiter()}

	live := newTestIngestSession("live")
	if err := svc.sessions.TryAcquire(live); err != nil {
		t.Fatalf("acquire live: %v", err)
	}
	if !svc.setCurrentSession(live) {
		t.Fatal("the live session must be published")
	}

	svc.clearCurrentSessionIfStale()

	if svc.currentSession != live {
		t.Fatal("a stale cleanup cleared the live session")
	}
}

// The cleanup still forgets a session that has ended.
func TestStaleOfflineClearsAFinishedSession(t *testing.T) {
	svc := &Service{sessions: ingest.NewArbiter()}

	ended := newTestIngestSession("ended")
	if err := svc.sessions.TryAcquire(ended); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if !svc.setCurrentSession(ended) {
		t.Fatal("the session must be published")
	}
	ended.Close(errors.New("done"))

	svc.clearCurrentSessionIfStale()

	if svc.currentSession != nil {
		t.Fatal("the cleanup must forget a session that ended")
	}
}
