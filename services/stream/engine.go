package stream

import (
	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/ingest"
)

// StreamEngine drives broadcaster ingest and transcoding. The local
// implementation runs every configured ingest protocol (RTMP, SRT) plus ffmpeg
// in-process; a future remote implementation manages a separate engine
// process. The stream service reacts to what it reports through StreamEvents,
// so the lifecycle owner never needs to know where ingest happens.
type StreamEngine interface {
	// Start begins accepting inbound broadcasts.
	Start() error
	// Stop force-disconnects any inbound broadcast and tears the engine down.
	Stop() error
}

// StreamEvents are the always-on, core-side reactions an engine drives as a
// broadcast comes and goes.
type StreamEvents interface {
	// StreamConnected reports a broadcast going live, carrying the settings
	// it is being transcoded with.
	StreamConnected(broadcast *models.CurrentBroadcast)
	// BroadcasterSet reports the inbound source's metadata (codec, etc.).
	BroadcasterSet(broadcaster models.Broadcaster)
	// StreamDisconnected reports the broadcast ending. err is the transcode
	// exit cause if any, nil for a clean end.
	StreamDisconnected(err error)
}

// IngestListener is the slice of an ingest service the local engine drives.
// Both the RTMP and SRT services satisfy it structurally.
type IngestListener interface {
	// Start binds the listener and reports authenticated sessions to
	// onConnect, plus optional source metadata to onBroadcaster.
	Start(onConnect func(ingest.Session), onBroadcaster func(models.Broadcaster))
	// Disconnect force-disconnects the current publisher, if any.
	Disconnect()
	// Stop closes the listener. Implementations must be safe to call once.
	Stop()
}

// localStreamEngine is the in-process engine: it binds every configured ingest
// listener and hands each inbound session to the stream service's connect
// handler, which spins up the transcoder.
type localStreamEngine struct {
	listeners     []IngestListener
	onConnect     func(ingest.Session)
	onBroadcaster func(models.Broadcaster)
}

// Compile-time guarantees for the local wiring.
var (
	_ StreamEngine = (*localStreamEngine)(nil)
	_ StreamEvents = (*Service)(nil)
)

// Start binds all ingest listeners and begins accepting connections. Each
// accept loop runs in its own goroutine.
func (e *localStreamEngine) Start() error {
	for _, listener := range e.listeners {
		current := listener
		go current.Start(e.onConnect, e.onBroadcaster)
	}
	return nil
}

// Stop force-disconnects the current inbound publisher and closes every
// ingest listener.
func (e *localStreamEngine) Stop() error {
	for _, listener := range e.listeners {
		listener.Disconnect()
		listener.Stop()
	}
	return nil
}
