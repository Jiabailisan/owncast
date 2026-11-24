package stream

import (
	"testing"
	"time"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/ingest"
	"github.com/owncast/owncast/services/webrtc"
)

// An install without a WebRTC service has no leg to plan, and HandleIngestSession
// does not read ahead for it either, so the SRT inspector can never publish the
// B-frame verdict a copy decision waits for. Planning must not sit out the full
// source-wait timeout before HLS starts: the resolver returns at once with an
// unknown source and no cancellation.
func TestResolveWebRTCSourceSkipsTheWaitWithoutAWebRTCService(t *testing.T) {
	// A nil WebRTC service is the configuration that used to wait 2.5s.
	svc := &Service{}

	// An SRT session that asks for copy is exactly what makes the wait start.
	// Nothing publishes a verdict for it and it never closes, so only the nil
	// guard can end the planning phase early.
	session := newSourceInfoSession("srt-no-webrtc")
	variants := []models.StreamOutputVariant{copyIntentVariant()}

	if session.Protocol() != ingest.ProtocolSRT {
		t.Fatalf("the fixture session has protocol %q, want %q", session.Protocol(), ingest.ProtocolSRT)
	}
	if !hasWebRTCCopyIntent(variants) {
		t.Fatal("the fixture variants do not ask WebRTC for copy")
	}
	if broadcaster := session.Broadcaster(); broadcaster != nil && broadcaster.StreamDetails.HasBFrames != nil {
		t.Fatal("the fixture session must not carry a B-frame verdict")
	}
	select {
	case <-session.Done():
		t.Fatal("the fixture session must stay open for the whole test")
	default:
	}

	start := time.Now()
	source, cancelled := svc.resolveWebRTCSource(session, variants, nil, nil)
	elapsed := time.Since(start)

	if elapsed >= 200*time.Millisecond {
		t.Fatalf("planning waited %s without a WebRTC service, want an immediate return", elapsed)
	}
	if cancelled {
		t.Fatal("an open session without a WebRTC service must not be reported as a cancellation")
	}
	if source != (webrtc.SourceInfo{}) {
		t.Fatalf("planning invented source metadata without a WebRTC service: %+v", source)
	}
}
