// Package rtmp is the inbound RTMP ingest service. It listens on the
// configured RTMP port, authenticates incoming streams with the shared
// ingest.StreamKeyValidator, reserves the cross-protocol publisher slot in
// ingest.Arbiter, and pipes the FLV-muxed bytes to the stream service via the
// callback supplied to Start.
package rtmp

import (
	"net"
	"sync"

	joy5rtmp "github.com/nareix/joy5/format/rtmp"

	"github.com/owncast/owncast/config"
	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/ingest"
)

// Service owns the RTMP listener and the single in-flight inbound
// connection (RTMP allows one broadcaster at a time; the cross-protocol
// single-publisher rule lives in ingest.Arbiter).
type Service struct {
	// setStreamAsConnected is invoked once at authenticated-connect time with
	// the authenticated session. Set by Start.
	setStreamAsConnected func(ingest.Session)

	// setBroadcaster is invoked when the inbound onMetaData arrives with the
	// parsed broadcaster details and the session they came from. Set by Start.
	setBroadcaster func(ingest.Session, models.Broadcaster)

	// configRepository is consulted at listener bind for the RTMP port.
	configRepository models.EngineConfig

	// cfg supplies the optional temporary stream key override set at
	// startup via the --streamkey CLI flag.
	cfg *config.Config

	// streamKeys validates the RTMP path against the configured stream keys.
	streamKeys *ingest.StreamKeyValidator

	// sessions is the cross-protocol single-publisher arbiter.
	sessions *ingest.Arbiter

	// isOnline reports whether the stream engine is still tearing a previous
	// session down. The arbiter releases its slot before that work finishes, so a
	// reconnect that only waited for the arbiter started while the old session was
	// still shutting down and was immediately disconnected again.
	isOnline func() bool

	// mu guards currentSession.
	mu sync.RWMutex
	// currentSession is the live inbound session, if any.
	currentSession *session

	// listenerMu guards listener, rtmpsListener, and rtmpServer.
	listenerMu sync.Mutex
	// listener is closed by Stop to end the plain RTMP accept loop.
	listener net.Listener
	// rtmpsListener is the optional TLS RTMP listener (nil when disabled).
	rtmpsListener net.Listener
	// rtmpServer is shared by the plain RTMP and optional RTMPS accept loops.
	rtmpServer *joy5rtmp.Server
}

// Deps is the explicit dependency contract for the RTMP service.
type Deps struct {
	ConfigRepository models.EngineConfig
	Config           *config.Config
	StreamKeys       *ingest.StreamKeyValidator
	Sessions         *ingest.Arbiter
	// IsOnline reports whether a stream is still running; used to wait for a
	// previous session to finish tearing down before accepting a reconnect.
	IsOnline func() bool
}

// New constructs an idle RTMP service. Call Start(...) to bind the
// listener and begin accepting connections.
func New(deps Deps) *Service {
	return &Service{
		configRepository: deps.ConfigRepository,
		cfg:              deps.Config,
		streamKeys:       deps.StreamKeys,
		sessions:         deps.Sessions,
		isOnline:         deps.IsOnline,
	}
}
