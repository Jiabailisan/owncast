package stream

import (
	"testing"
	"time"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/ingest"
	"github.com/owncast/owncast/services/webrtc"
)

// main.go always constructs the WebRTC service, so "a service exists" is not the
// delivery switch: the service reports it through Enabled(), and OpenMediaLeg and
// the WHEP handler both refuse while it is false. An SRT stream that still carries
// copy intent must therefore neither be read ahead for nor wait for a B-frame
// verdict when delivery is off: nothing would consume the verdict, and the wait
// would only delay the HLS start by the full source-wait timeout.
func TestWebRTCCopyPlanIsNotPendingWhenDeliveryIsDisabled(t *testing.T) {
	provider := &planningProvider{disabled: true}
	svc := &Service{webRTC: provider}

	// A service is injected, as in production; it reports delivery as off.
	if svc.webRTC == nil {
		t.Fatal("the fixture must inject a WebRTC service: the nil-service case is already covered")
	}
	if svc.webRTC.Enabled() {
		t.Fatal("the fixture provider must report WebRTC delivery as disabled")
	}

	session := newSourceInfoSession("srt-delivery-disabled")
	variants := []models.StreamOutputVariant{copyIntentVariant()}

	// The stream still asks for copy and would wait if delivery were on.
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

	// The read-ahead gate: no prefetch reader, so no byte reaches the inspector and
	// no budget signal can be produced.
	if svc.webRTCCopyPlanPending(session, variants) {
		t.Fatal("a stream with WebRTC delivery disabled still planned a copy decision")
	}

	// The wait gate: planning returns at once with an unknown source and no
	// cancellation instead of sitting out the source-wait timeout.
	start := time.Now()
	source, cancelled := svc.resolveWebRTCSource(session, variants, nil, nil)
	elapsed := time.Since(start)

	if elapsed >= 200*time.Millisecond {
		t.Fatalf("planning waited %s with WebRTC delivery disabled, want an immediate return", elapsed)
	}
	if cancelled {
		t.Fatal("an open session with WebRTC delivery disabled must not be reported as a cancellation")
	}
	if source != (webrtc.SourceInfo{}) {
		t.Fatalf("planning invented source metadata with WebRTC delivery disabled: %+v", source)
	}

	// Guard against the gate collapsing to "always false": the same stream with
	// delivery on does plan the copy decision.
	enabled := &Service{webRTC: &planningProvider{}}
	if !enabled.webRTCCopyPlanPending(session, variants) {
		t.Fatal("a stream with WebRTC delivery on must still plan the copy decision")
	}
}
