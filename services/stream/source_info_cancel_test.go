package stream

import (
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/ingest"
)

// planningProvider records whether the WebRTC plan was allowed to publish
// variants or open media legs.
type planningProvider struct {
	mu          sync.Mutex
	setVariants int
	openLegs    int
}

func (p *planningProvider) SetVariants([]models.StreamOutputVariant) (uint64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.setVariants++

	return 1, nil
}

func (p *planningProvider) OpenMediaLeg(int, uint64) (WebRTCMediaLeg, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.openLegs++

	return nil, errors.New("no leg in this test")
}

func (p *planningProvider) CloseMediaLeg(int) error       { return nil }
func (p *planningProvider) VariantGeneration() uint64     { return 1 }
func (p *planningProvider) DisableVariants(uint64, []int) {}

func (p *planningProvider) calls() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.setVariants, p.openLegs
}

// cancellableSession is an SRT session whose media never arrives: the read-ahead
// goroutine blocks on the pipe until the session ends.
type cancellableSession struct {
	*ingest.SessionBase

	ready  chan struct{}
	reader io.Reader
}

func (s *cancellableSession) Reader() io.Reader { return s.reader }

func (s *cancellableSession) SourceInfoReady() <-chan struct{} { return s.ready }

func newCancellableSession(id string) *cancellableSession {
	reader, writer := io.Pipe()
	base := ingest.NewSessionBase(id, ingest.ProtocolSRT, nil, "key", ingest.MPEGTSInputFormat(), func(error) {
		_ = writer.Close()
	})

	return &cancellableSession{SessionBase: base, ready: make(chan struct{}), reader: reader}
}

// A publisher that goes away while the copy decision is pending must not be
// published: no online announcement, no variants, no media legs, no transcoder
// and no thumbnail generator. The publish boundary already refuses such a
// session, so the startup flow stops before any of it.
func TestHandleIngestSessionStopsWhenTheSessionEndsDuringTheWait(t *testing.T) {
	repo := newStreamStatsTestRepo(t)
	if err := repo.SetStreamOutputVariants([]models.StreamOutputVariant{copyIntentVariant()}); err != nil {
		t.Fatalf("seed output variants: %v", err)
	}

	arbiter := ingest.NewArbiter()
	provider := &planningProvider{}
	svc := &Service{
		configRepository: repo,
		sessions:         arbiter,
		webRTC:           provider,
		stats:            &models.Stats{Viewers: map[string]*models.Viewer{}},
	}

	session := newCancellableSession("waiting")
	if err := arbiter.TryAcquire(session); err != nil {
		t.Fatalf("acquire: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- svc.HandleIngestSession(session) }()

	// Let the copy decision start waiting, then end the publisher.
	time.Sleep(50 * time.Millisecond)
	session.Close(errors.New("publisher disconnected"))
	arbiter.Release(session)

	var err error
	select {
	case err = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("HandleIngestSession did not return after the session ended")
	}

	if !errors.Is(err, errSessionCancelled) {
		t.Fatalf("HandleIngestSession returned %v, want errSessionCancelled", err)
	}
	if setVariants, openLegs := provider.calls(); setVariants != 0 || openLegs != 0 {
		t.Fatalf("a cancelled session planned %d variant sets and %d media legs", setVariants, openLegs)
	}
	if svc.GetCurrentSession() != nil {
		t.Fatal("a cancelled session must not be published as the current stream")
	}
	if svc.stats.StreamConnected {
		t.Fatal("a cancelled session must not announce the stream online")
	}

	svc.transcoderMu.RLock()
	transcoder := svc.transcoder
	svc.transcoderMu.RUnlock()
	if transcoder != nil {
		t.Fatal("a cancelled session must not start a transcoder")
	}
	if svc.thumbnailGen != nil {
		t.Fatal("a cancelled session must not start the thumbnail generator")
	}

	// The next publisher takes the slot and the cancelled startup must not clear
	// or overwrite it afterwards.
	next := newTestIngestSession("next")
	if err := arbiter.TryAcquire(next); err != nil {
		t.Fatalf("acquire next: %v", err)
	}
	if !svc.setCurrentSession(next) {
		t.Fatal("the next session must be publishable")
	}
	svc.clearCurrentSessionIfStale()
	if svc.GetCurrentSession() != next {
		t.Fatal("the cancelled startup cleared the next session")
	}
}
