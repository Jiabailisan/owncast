package stream

import (
	"testing"
	"time"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/ingest"
)

// fakeIngestListener is a stand-in for *rtmp.Service / *srt.Service that
// records how the local engine drives it, without binding a real socket.
type fakeIngestListener struct {
	started       chan struct{}
	onConnect     func(ingest.Session)
	onBroadcaster func(models.Broadcaster)
	disconnected  bool
}

func (f *fakeIngestListener) Start(onConnect func(ingest.Session), onBroadcaster func(models.Broadcaster)) {
	f.onConnect = onConnect
	f.onBroadcaster = onBroadcaster
	close(f.started)
}

func (f *fakeIngestListener) Disconnect() { f.disconnected = true }

var _ IngestListener = (*fakeIngestListener)(nil)

// TestLocalStreamEngineDrivesIngestListeners proves the local engine starts
// every configured ingest listener with the service's own callbacks, and tears
// them all down on Stop.
func TestLocalStreamEngineDrivesIngestListeners(t *testing.T) {
	rtmpListener := &fakeIngestListener{started: make(chan struct{})}
	srtListener := &fakeIngestListener{started: make(chan struct{})}

	connectCalled := false
	broadcasterCalled := false
	e := &localStreamEngine{
		listeners:     []IngestListener{rtmpListener, srtListener},
		onConnect:     func(ingest.Session) { connectCalled = true },
		onBroadcaster: func(models.Broadcaster) { broadcasterCalled = true },
	}

	if err := e.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	for name, listener := range map[string]*fakeIngestListener{"rtmp": rtmpListener, "srt": srtListener} {
		select {
		case <-listener.started:
		case <-time.After(2 * time.Second):
			t.Fatalf("engine never started the %s listener", name)
		}
	}

	// The listeners must have received the engine's own callbacks verbatim.
	rtmpListener.onConnect(nil)
	rtmpListener.onBroadcaster(models.Broadcaster{})
	if !connectCalled {
		t.Error("engine did not wire onConnect through to the listener")
	}
	if !broadcasterCalled {
		t.Error("engine did not wire onBroadcaster through to the listener")
	}

	if err := e.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !rtmpListener.disconnected || !srtListener.disconnected {
		t.Error("engine did not disconnect every listener")
	}
}
