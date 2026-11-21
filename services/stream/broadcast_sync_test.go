package stream

import (
	"sync"
	"testing"

	"github.com/owncast/owncast/models"
)

// The broadcast settings are read by the playlist, metrics and admin paths while a
// stream goes live or offline; this exercises the accessor against concurrent
// writers under -race.
func TestBroadcastReadsAreSynchronised(t *testing.T) {
	svc := &Service{}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			svc.statsMu.Lock()
			svc.currentBroadcast = &models.CurrentBroadcast{}
			svc.statsMu.Unlock()

			svc.statsMu.Lock()
			svc.currentBroadcast = nil
			svc.statsMu.Unlock()
		}
	}()

	for i := 0; i < 2000; i++ {
		_ = svc.GetCurrentBroadcast()
	}

	wg.Wait()
}
