package stream

import (
	"io"
	"testing"
	"time"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/ingest"
	"github.com/owncast/owncast/services/webrtc"
)

// sourceInfoSession is an SRT ingest session that also reports the moment the
// MPEG-TS inspector published the B-frame verdict. That signal is what lets the
// stream service plan WebRTC copy legs without waiting for a statistics tick.
type sourceInfoSession struct {
	*ingest.SessionBase

	ready chan struct{}
}

func newSourceInfoSession(id string) *sourceInfoSession {
	return &sourceInfoSession{
		SessionBase: ingest.NewSessionBase(id, ingest.ProtocolSRT, nil, "key", ingest.MPEGTSInputFormat(), nil),
		ready:       make(chan struct{}),
	}
}

// Reader is the live media stream. These tests never consume it.
func (s *sourceInfoSession) Reader() io.Reader { return nil }

// SourceInfoReady implements sourceInfoReadyWaiter.
func (s *sourceInfoSession) SourceInfoReady() <-chan struct{} { return s.ready }

// sourceInfoBroadcaster builds the metadata the SRT inspector publishes. A nil
// hasBFrames means "not decided yet".
func sourceInfoBroadcaster(hasBFrames *bool) *models.Broadcaster {
	return &models.Broadcaster{
		Time: time.Now(),
		StreamDetails: models.InboundStreamDetails{
			VideoCodec:   "h264",
			VideoProfile: "constrained-baseline",
			VideoLevel:   "3.1",
			Width:        1280,
			Height:       720,
			HasBFrames:   hasBFrames,
		},
	}
}

// copyIntentVariant is an HLS passthrough variant that explicitly asks WebRTC for
// stream copy, so the plan depends on the source B-frame verdict.
func copyIntentVariant() models.StreamOutputVariant {
	return models.StreamOutputVariant{
		Name: "copy",
		Outputs: &models.VariantOutputs{
			HLS:    &models.ProtocolOutputSettings{Enabled: models.BoolPtr(true)},
			WebRTC: &models.ProtocolOutputSettings{Enabled: models.BoolPtr(true), Mode: models.OutputModeCopy},
		},
		VideoBitrate:       2500,
		Framerate:          30,
		IsVideoPassthrough: true,
	}
}

// The verdict arrives from the ingest read path. No statistics sampler runs in
// this test, so returning quickly proves the wait is driven by the published
// verdict and not by a periodic tick.
func TestAwaitSourceInfoReturnsOnVerdictWithoutStatsTick(t *testing.T) {
	session := newSourceInfoSession("srt-verdict")

	go func() {
		time.Sleep(50 * time.Millisecond)
		session.SetBroadcaster(sourceInfoBroadcaster(models.BoolPtr(false)))
		close(session.ready)
	}()

	start := time.Now()
	broadcaster, cancelled := awaitSourceInfo(session, nil, 2500*time.Millisecond)
	elapsed := time.Since(start)

	if cancelled {
		t.Fatal("a published verdict must not be reported as a cancellation")
	}
	if elapsed >= time.Second {
		t.Fatalf("awaitSourceInfo waited %s for a verdict published after 50ms", elapsed)
	}
	if broadcaster == nil {
		t.Fatal("awaitSourceInfo returned no broadcaster after the verdict was published")
	}
	if broadcaster.StreamDetails.HasBFrames == nil {
		t.Fatal("the returned broadcaster does not carry the B-frame verdict")
	}
	if *broadcaster.StreamDetails.HasBFrames {
		t.Fatal("the returned broadcaster reports B-frames for a source without any")
	}
}

// When the read-ahead budget is spent nothing else can reach the inspector, so
// the wait must end there instead of burning the full timeout. The verdict stays
// unknown and the existing rules still fall back to transcode.
func TestAwaitSourceInfoStopsWhenPrefetchBudgetSpent(t *testing.T) {
	session := newSourceInfoSession("srt-budget")
	budgetSpent := make(chan struct{})

	go func() {
		time.Sleep(50 * time.Millisecond)
		close(budgetSpent)
	}()

	start := time.Now()
	broadcaster, cancelled := awaitSourceInfo(session, budgetSpent, 2500*time.Millisecond)
	elapsed := time.Since(start)

	if cancelled {
		t.Fatal("a spent prefetch budget must not be reported as a cancellation")
	}
	if elapsed >= time.Second {
		t.Fatalf("awaitSourceInfo waited %s after the prefetch budget was spent", elapsed)
	}
	if broadcaster != nil && broadcaster.StreamDetails.HasBFrames != nil {
		t.Fatal("the wait invented a B-frame verdict that was never published")
	}

	variant := copyIntentVariant()
	if !hasWebRTCCopyIntent([]models.StreamOutputVariant{variant}) {
		t.Fatal("the fixture variant does not ask WebRTC for copy")
	}

	decision, err := webrtc.ResolveWebRTCLeg(webrtc.SourceInfo{Protocol: "srt"}, variant, models.WebRTCConfig{})
	if err != nil {
		t.Fatalf("resolving the copy leg for an unknown source failed: %v", err)
	}
	if decision.Copy || decision.Mode != models.OutputModeTranscode {
		t.Fatalf("an unknown source resolved to mode %q (copy=%t), want %q",
			decision.Mode, decision.Copy, models.OutputModeTranscode)
	}
}

// A publisher that goes away while the verdict is pending must not keep the
// stream starting until the timeout expires.
func TestAwaitSourceInfoReturnsOnSessionClose(t *testing.T) {
	session := newSourceInfoSession("srt-closed")

	go func() {
		time.Sleep(50 * time.Millisecond)
		session.Close(nil)
	}()

	start := time.Now()
	broadcaster, cancelled := awaitSourceInfo(session, nil, 2500*time.Millisecond)
	elapsed := time.Since(start)

	if !cancelled {
		t.Fatal("a session that ended during the wait must be reported as a cancellation")
	}
	if elapsed >= time.Second {
		t.Fatalf("awaitSourceInfo waited %s after the session was closed", elapsed)
	}
	if broadcaster != nil {
		t.Fatal("a closed session without a verdict returned metadata")
	}
}

// A source that keeps feeding the inspector without ever deciding falls through
// to the timeout, and a nil budget channel must simply never fire.
func TestAwaitSourceInfoHonorsTimeout(t *testing.T) {
	session := newSourceInfoSession("srt-timeout")

	start := time.Now()
	broadcaster, cancelled := awaitSourceInfo(session, nil, 100*time.Millisecond)
	elapsed := time.Since(start)

	if cancelled {
		t.Fatal("a timeout must not be reported as a cancellation")
	}
	if elapsed < 100*time.Millisecond {
		t.Fatalf("awaitSourceInfo returned after %s, before its 100ms timeout", elapsed)
	}
	if elapsed >= time.Second {
		t.Fatalf("awaitSourceInfo ignored its 100ms timeout and waited %s", elapsed)
	}
	if broadcaster != nil {
		t.Fatal("a timed out wait returned metadata")
	}
}

func TestAwaitSourceInfoWithReasonDistinguishesTimeoutAndVerdict(t *testing.T) {
	timeoutSession := newSourceInfoSession("srt-timeout-reason")
	_, cancelled, reason := awaitSourceInfoWithReason(timeoutSession, nil, 25*time.Millisecond)
	if cancelled || reason != "timeout" {
		t.Fatalf("timeout wait = (cancelled=%v, reason=%q), want (false, timeout)", cancelled, reason)
	}

	budgetSession := newSourceInfoSession("srt-budget-reason")
	budgetSpent := make(chan struct{})
	close(budgetSpent)
	_, cancelled, reason = awaitSourceInfoWithReason(budgetSession, budgetSpent, time.Second)
	if cancelled || reason != "budget" {
		t.Fatalf("budget wait = (cancelled=%v, reason=%q), want (false, budget)", cancelled, reason)
	}

	verdictSession := newSourceInfoSession("srt-verdict-reason")
	verdictSession.SetBroadcaster(sourceInfoBroadcaster(models.BoolPtr(false)))
	_, cancelled, reason = awaitSourceInfoWithReason(verdictSession, nil, time.Second)
	if cancelled || reason != "verdict" {
		t.Fatalf("verdict wait = (cancelled=%v, reason=%q), want (false, verdict)", cancelled, reason)
	}
}
