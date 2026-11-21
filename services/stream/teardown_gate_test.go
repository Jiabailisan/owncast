package stream

import (
	"testing"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/ingest"
)

// A publish that arrives while the previous broadcast is still being cleaned up is
// refused: the shared state it would interleave with is still in use.
func TestPublishIsRefusedWhileThePreviousTeardownRuns(t *testing.T) {
	arbiter := ingest.NewArbiter()
	svc := &Service{sessions: arbiter, stats: &models.Stats{Viewers: map[string]*models.Viewer{}}}

	session := newTestIngestSession("next")
	if err := arbiter.TryAcquire(session); err != nil {
		t.Fatalf("acquire: %v", err)
	}

	svc.tearingDown.Store(true)
	defer svc.tearingDown.Store(false)

	if svc.setCurrentSession(session) {
		t.Fatal("a session must not be published while the previous teardown is running")
	}
	if svc.currentSession != nil {
		t.Fatal("the refused session was published anyway")
	}

	// Once the teardown finished the same session can be published.
	svc.tearingDown.Store(false)
	if !svc.setCurrentSession(session) {
		t.Fatal("the session must be published after the teardown finished")
	}
}
