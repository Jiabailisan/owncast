package srt

import (
	"testing"

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
