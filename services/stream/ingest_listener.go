package stream

import (
	"context"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/ingest"
)

// contextIngestStopTimeout bounds how long engine shutdown waits for an ingest
// listener to stop, so a wedged media handler cannot hang server shutdown.
const contextIngestStopTimeout = 5 * time.Second

// ContextIngestService is the lifecycle shape of ingest services that own a
// context and deliver accepted sessions to a handler configured on the service
// (the SRT service). The RTMP service satisfies IngestListener directly; SRT is
// adapted through ContextIngestListener so both are started identically by the
// local engine.
type ContextIngestService interface {
	// Start binds the listener and starts its accept loop. It returns once the
	// listener is running (not when it shuts down); the loop is owned by the
	// service. Accepted sessions are delivered to the handler configured on
	// the service (see the SRT Deps.Handler).
	Start(ctx context.Context) error
	// Stop tears the listener down and waits for its goroutines.
	Stop(ctx context.Context) error
	// Disconnect force-disconnects the current publisher.
	Disconnect() bool
}

// ContextIngestListener adapts a ContextIngestService to the engine's
// IngestListener contract.
type ContextIngestListener struct {
	service ContextIngestService

	mu     sync.Mutex
	cancel context.CancelFunc
}

// NewContextIngestListener wraps an ingest service for the local engine.
func NewContextIngestListener(service ContextIngestService) *ContextIngestListener {
	return &ContextIngestListener{service: service}
}

// Start implements IngestListener.
func (l *ContextIngestListener) Start(_ func(ingest.Session), _ func(models.Broadcaster)) {
	// Sessions are handed to the handler configured on the service itself; the
	// engine-wide onConnect callback is not used by protocol services that own
	// their handler (SRT).
	ctx, cancel := context.WithCancel(context.Background())
	l.mu.Lock()
	l.cancel = cancel
	l.mu.Unlock()

	if err := l.service.Start(ctx); err != nil {
		log.Errorln("ingest listener failed to start:", err)
	}
}

// Disconnect implements IngestListener.
func (l *ContextIngestListener) Disconnect() {
	l.service.Disconnect()
}

// Stop implements IngestListener. It is safe to call once and is bounded.
func (l *ContextIngestListener) Stop() {
	l.mu.Lock()
	cancel := l.cancel
	l.cancel = nil
	l.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	ctx, cancelStop := context.WithTimeout(context.Background(), contextIngestStopTimeout)
	defer cancelStop()
	if err := l.service.Stop(ctx); err != nil {
		log.Errorln("ingest listener failed to stop:", err)
	}
}
