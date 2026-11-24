package stream

import (
	"testing"
	"time"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/ingest"
	"github.com/owncast/owncast/services/webrtc"
)

// The startup path asks "must this stream wait for the copy verdict?" twice: once
// in HandleIngestSession, to decide whether to read ahead, and once in
// waitForSourceInfo, to decide whether to block. The admin applies a delivery
// change immediately, so the two reads can disagree. The state this test pins is
// the one that used to cost 2.5s: the first read saw delivery off, so no
// read-ahead was started (budgetSpent is nil), and by the second read delivery is
// on. Nothing feeds the inspector, so the verdict never arrives and the wait can
// only run into its timeout.
//
// Constructing the two reads is not necessary to express that state, and it would
// make the test depend on the timing of the switch: "delivery on with budgetSpent
// nil" is exactly what the mismatched path produces at the resolver, because
// budgetSpent is non-nil if and only if HandleIngestSession started the read-ahead
// (a disabled first read leaves it nil). The plan gate alone is true here, which
// is why the wait used to start.
func TestResolveWebRTCSourceSkipsTheWaitWhenNoReadAheadStarted(t *testing.T) {
	// Delivery is on at the second read, as after an admin turned it on.
	svc := &Service{webRTC: &planningProvider{}}

	session := newSourceInfoSession("srt-no-read-ahead")
	variants := []models.StreamOutputVariant{copyIntentVariant()}

	if !svc.webRTCDeliveryEnabled() {
		t.Fatal("the fixture must have WebRTC delivery enabled")
	}
	if session.Protocol() != ingest.ProtocolSRT {
		t.Fatalf("the fixture session has protocol %q, want %q", session.Protocol(), ingest.ProtocolSRT)
	}
	if !hasWebRTCCopyIntent(variants) {
		t.Fatal("the fixture variants do not ask WebRTC for copy")
	}
	// The copy decision is still pending, so the wait gate on its own would let
	// the stream block.
	if !svc.webRTCCopyPlanPending(session, variants) {
		t.Fatal("the fixture must still have a pending copy decision")
	}
	// No read-ahead for this stream: budgetSpent is nil, exactly as
	// HandleIngestSession leaves it when its own gate saw delivery off. Nothing
	// publishes a verdict and the session stays open, so only the missing
	// read-ahead can end the wait early.
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
		t.Fatalf("planning waited %s without a read-ahead, want an immediate return: the wait is for the verdict the read-ahead publishes", elapsed)
	}
	if cancelled {
		t.Fatal("an open session without a read-ahead must not be reported as a cancellation")
	}
	// The source stays unknown (protocol only); the eligibility rules treat that
	// as "transcode rather than copy".
	if source != (webrtc.SourceInfo{Protocol: string(session.Protocol())}) {
		t.Fatalf("planning returned source metadata beyond the protocol: %+v", source)
	}
}
