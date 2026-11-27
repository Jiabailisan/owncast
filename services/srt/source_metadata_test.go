package srt

import (
	"testing"
	"time"

	"github.com/owncast/owncast/services/ingest/mpegts"
)

// inspectedSource is a decided snapshot of the MPEG-TS inspector.
func inspectedSource(hasBFrames bool) mpegts.SourceInfo {
	return mpegts.SourceInfo{
		VideoCodec:    "h264",
		Profile:       "high",
		Level:         "4.0",
		Width:         1920,
		Height:        1080,
		HasBFrames:    hasBFrames,
		BFrameChecked: true,
	}
}

// The statistics sampler and the ingest read path both publish source metadata
// from snapshots they took at different moments. When the sampler's snapshot was
// taken before the B-frame verdict and lands after it, the published verdict must
// survive: a copy decision may already be waking up on it.
func TestPublishedBFrameVerdictIsNotReplacedByAnOlderSnapshot(t *testing.T) {
	s := newSession(nil, "key", 5, false, func(error) {})

	// The read path publishes the verdict the inspector reached last.
	s.publishSourceMetadata(inspectedSource(true))

	// The sampler's older snapshot, taken before that verdict, lands afterwards.
	s.publishSourceMetadata(inspectedSource(false))

	broadcaster := s.Broadcaster()
	if broadcaster == nil {
		t.Fatal("no source metadata was published")
	}
	if broadcaster.StreamDetails.HasBFrames == nil {
		t.Fatal("the published B-frame verdict disappeared")
	}
	if !*broadcaster.StreamDetails.HasBFrames {
		t.Fatal("an older snapshot replaced the published B-frame verdict")
	}

	select {
	case <-s.SourceInfoReady():
	default:
		t.Fatal("the verdict was published without announcing it")
	}
}

// The guard must not freeze the verdict in the other direction: a source that was
// called "no B-frames" and later shows a B slice still updates.
func TestPublishedNoBFrameVerdictCanStillBecomeTrue(t *testing.T) {
	s := newSession(nil, "key", 5, false, func(error) {})

	s.publishSourceMetadata(inspectedSource(false))
	s.publishSourceMetadata(inspectedSource(true))

	broadcaster := s.Broadcaster()
	if broadcaster == nil || broadcaster.StreamDetails.HasBFrames == nil {
		t.Fatal("no B-frame verdict was published")
	}
	if !*broadcaster.StreamDetails.HasBFrames {
		t.Fatal("a later B-frame verdict did not replace the earlier one")
	}
}

// A snapshot without a verdict must not take a published one away either.
func TestPublishedVerdictIsNotReplacedByAnUndecidedSnapshot(t *testing.T) {
	s := newSession(nil, "key", 5, false, func(error) {})

	s.publishSourceMetadata(inspectedSource(false))

	undecided := inspectedSource(false)
	undecided.BFrameChecked = false
	s.publishSourceMetadata(undecided)

	broadcaster := s.Broadcaster()
	if broadcaster == nil || broadcaster.StreamDetails.HasBFrames == nil {
		t.Fatal("an undecided snapshot removed the published verdict")
	}
}

func TestSRTBroadcasterTimeStaysAnchoredToSessionStart(t *testing.T) {
	s := newSession(nil, "key", 5, false, func(error) {})
	startedAt := s.StartedAt()

	first := inspectedSource(false)
	first.VideoBitrateValid = true
	first.VideoBitrateKbps = 6000
	s.publishSourceMetadata(first)

	time.Sleep(15 * time.Millisecond)

	second := first
	second.VideoBitrateKbps = 7500
	s.publishSourceMetadata(second)

	broadcaster := s.Broadcaster()
	if broadcaster == nil {
		t.Fatal("expected broadcaster")
	}
	if !broadcaster.Time.Equal(startedAt) {
		t.Fatalf("broadcaster.Time = %v, want immutable session StartedAt %v", broadcaster.Time, startedAt)
	}
}

// A mid-GOP H.264 stream can reach 60 P-frames (HasBFrames=false) before the
// first keyframe delivers SPS (profile/level/resolution). SourceInfoReady must
// stay open until SPS arrives so WebRTC copy planning does not prematurely fall
// back to transcode with unknown profile/resolution/level.
func TestNoBFrameVerdictWithoutSPSWaitsForSPSBeforeClosingSourceReady(t *testing.T) {
	s := newSession(nil, "key", 5, false, func(error) {})

	midGOP := mpegts.SourceInfo{
		VideoCodec:    "h264",
		HasBFrames:    false,
		BFrameChecked: true,
	}
	s.publishSourceMetadata(midGOP)

	select {
	case <-s.SourceInfoReady():
		t.Fatal("SourceInfoReady closed before SPS profile/level/resolution arrived")
	default:
	}

	// Once SPS arrives on the next keyframe, SourceInfoReady closes immediately.
	s.publishSourceMetadata(inspectedSource(false))
	select {
	case <-s.SourceInfoReady():
	default:
		t.Fatal("SourceInfoReady did not close after SPS metadata arrived")
	}
}

// Confirmed B-frames immediately disqualify WebRTC copy even without SPS.
func TestConfirmedBFramesWithoutSPSSettlesImmediately(t *testing.T) {
	s := newSession(nil, "key", 5, false, func(error) {})

	s.publishSourceMetadata(mpegts.SourceInfo{
		VideoCodec:    "h264",
		HasBFrames:    true,
		BFrameChecked: true,
	})

	select {
	case <-s.SourceInfoReady():
	default:
		t.Fatal("SourceInfoReady must close immediately when B-frames are confirmed")
	}
}
