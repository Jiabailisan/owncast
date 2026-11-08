package stream

import (
	"context"

	log "github.com/sirupsen/logrus"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/ingest"
)

// ContextIngestService is the lifecycle shape of ingest services that own a
// context and a session handler (the SRT service). The RTMP service satisfies
// IngestListener directly; SRT is adapted through ContextIngestListener so both
// are started identically by the local engine.
type ContextIngestService interface {
	// Start binds the listener and blocks until the context is cancelled or
	// the service stops. Accepted sessions are delivered to the handler
	// configured on the service (see the SRT Deps.Handler).
	Start(ctx context.Context) error
	// Stop tears the listener down.
	Stop(ctx context.Context) error
	// Disconnect force-disconnects the current publisher.
	Disconnect() bool
}

// ContextIngestListener adapts a ContextIngestService to the engine's
// IngestListener contract.
type ContextIngestListener struct {
	service ContextIngestService

	ctx    context.Context
	cancel context.CancelFunc
}

// NewContextIngestListener wraps an ingest service for the local engine.
func NewContextIngestListener(service ContextIngestService) *ContextIngestListener {
	return &ContextIngestListener{service: service}
}

// Start implements IngestListener.
func (l *ContextIngestListener) Start(onConnect func(ingest.Session), _ func(models.Broadcaster)) {
	ctx, cancel := context.WithCancel(context.Background())
	l.ctx = ctx
	l.cancel = cancel

	_ = onConnect // the service delivers sessions to its configured handler
	if err := l.service.Start(ctx); err != nil {
		log.Errorln("ingest listener failed to start:", err)
	}
}

// Disconnect implements IngestListener.
func (l *ContextIngestListener) Disconnect() {
	l.service.Disconnect()
}

// Stop implements IngestListener.
func (l *ContextIngestListener) Stop() {
	if l.cancel != nil {
		l.cancel()
		l.cancel = nil
	}
	if err := l.service.Stop(context.Background()); err != nil {
		log.Errorln("ingest listener failed to stop:", err)
	}
}
