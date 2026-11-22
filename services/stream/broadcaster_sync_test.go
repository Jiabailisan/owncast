package stream

import (
	"sync"
	"testing"

	"github.com/owncast/owncast/models"
)

// The source metadata used for HLS quality must belong to the same broadcast as the
// viewer snapshot, or a viewer would see the previous stream's numbers.
func TestSourceDetailsFollowTheSnapshotEpoch(t *testing.T) {
	svc := &Service{stats: &models.Stats{StreamConnected: true, Viewers: map[string]*models.Viewer{}}}
	svc.broadcastEpoch = 1
	svc.BroadcasterSet(models.Broadcaster{StreamDetails: models.InboundStreamDetails{VideoBitrate: 4321}})

	if details := svc.sourceStreamDetailsFor(1, nil); details == nil || details.VideoBitrate != 4321 {
		t.Fatalf("current broadcast details = %+v, want the recorded metadata", details)
	}

	// The next broadcast starts: the old metadata no longer describes it.
	svc.statsMu.Lock()
	svc.broadcastEpoch = 2
	svc.statsMu.Unlock()

	if details := svc.sourceStreamDetailsFor(1, nil); details != nil {
		t.Fatalf("stale epoch returned %+v, want nothing", details)
	}
}

// Ending a stream clears the metadata with the broadcast, so the next stream does not
// inherit it.
func TestOfflineClearsTheBroadcaster(t *testing.T) {
	svc := &Service{stats: &models.Stats{StreamConnected: true, Viewers: map[string]*models.Viewer{}}}
	svc.broadcastEpoch = 1
	svc.BroadcasterSet(models.Broadcaster{StreamDetails: models.InboundStreamDetails{VideoBitrate: 1200}})

	svc.beginOffline()

	svc.statsMu.Lock()
	stored, epoch := svc.broadcaster, svc.broadcasterEpoch
	svc.statsMu.Unlock()

	if stored != nil || epoch != 0 {
		t.Fatalf("metadata survived the teardown: %+v epoch %d", stored, epoch)
	}
}

// Reads and writes are synchronised: the metadata is read by the status, quality and
// admin paths while the engine records it.
func TestBroadcasterAccessIsSynchronised(t *testing.T) {
	svc := &Service{stats: &models.Stats{Viewers: map[string]*models.Viewer{}}}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			svc.BroadcasterSet(models.Broadcaster{StreamDetails: models.InboundStreamDetails{VideoBitrate: i}})
		}
	}()

	for i := 0; i < 2000; i++ {
		_ = svc.GetBroadcaster()
		_ = svc.sourceStreamDetailsFor(0, nil)
	}

	wg.Wait()
}
