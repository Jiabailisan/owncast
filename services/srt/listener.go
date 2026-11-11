package srt

import (
	"context"
	"errors"
	"fmt"
	"time"

	gosrt "github.com/datarhei/gosrt"
	log "github.com/sirupsen/logrus"

	"github.com/owncast/owncast/models"
)

// acceptLoopShutdownTimeout bounds how long a listener rebind waits for the
// old accept loop to exit after the socket closed.
const acceptLoopShutdownTimeout = 5 * time.Second

// bindListener builds the gosrt listener for a configuration snapshot.
func (s *Service) bindListener(cfg models.SRTConfig) (gosrt.Listener, error) {
	listenerCfg, err := BuildListenerConfig(cfg)
	if err != nil {
		return nil, err
	}
	listenerCfg.Logger = s.gosrtLogger

	address := ListenAddress(cfg)
	listener, err := gosrt.Listen("srt", address, listenerCfg)
	if err != nil {
		return nil, fmt.Errorf("srt: listen on %s: %w", address, err)
	}
	return listener, nil
}

// acceptLoop accepts connection requests until the listener closes or the
// service context is canceled. Every request is handed to an authenticated
// handler goroutine tracked by the service WaitGroup.
func (s *Service) acceptLoop(ctx context.Context, listener gosrt.Listener) {
	for {
		request, err := listener.Accept2()
		if err != nil {
			if errors.Is(err, gosrt.ErrListenerClosed) {
				return
			}
			select {
			case <-ctx.Done():
				return
			default:
			}
			s.logger.WithError(err).Error("SRT accept loop failed; the listener is no longer accepting")

			// The loop is gone, so the listener must stop reporting running and
			// listening: publishers would otherwise hang until their handshake timed
			// out while the status page claimed everything was fine.
			s.setFailure(err)
			s.clearListener()

			return
		}

		if !s.spawn(func() { s.handleRequest(request) }) {
			// The service is stopping; reject the pending request so the
			// gosrt listener does not leak its reservation.
			request.Reject(wireRejectSystem)
			return
		}
	}
}

// handleRequest runs one connection request through the authentication
// pipeline and starts the accepted session.
func (s *Service) handleRequest(request gosrt.ConnRequest) {
	if !s.pendingHandshakes.TryAcquire() {
		s.reject(request, wireRejectResource, reasonHandshakeCapExceeded, log.Fields{
			logFieldRemote: remoteString(request.RemoteAddr()),
		})
		return
	}

	sess, ok := s.authenticate(request)
	s.pendingHandshakes.Release()
	if !ok {
		return
	}

	s.startSession(sess)
}
