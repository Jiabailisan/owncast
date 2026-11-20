package stream

import (
	"testing"

	"github.com/owncast/owncast/services/transcoder"
)

// A supervisor belonging to a session that already ended must not clear the
// process reference a newer session installed, otherwise the replacement process
// of the next stream is left unreferenced and cannot be stopped.
func TestClearWebRTCTranscoderRespectsOwnership(t *testing.T) {
	svc := &Service{}
	stale := &transcoder.Transcoder{}
	current := &transcoder.Transcoder{}

	svc.setWebRTCTranscoder(current)
	svc.clearWebRTCTranscoder(stale)

	if got := svc.getWebRTCTranscoder(); got != current {
		t.Fatal("a stale supervisor cleared the current process reference")
	}

	svc.clearWebRTCTranscoder(current)

	if got := svc.getWebRTCTranscoder(); got != nil {
		t.Fatal("the owning supervisor did not clear its own reference")
	}
}
