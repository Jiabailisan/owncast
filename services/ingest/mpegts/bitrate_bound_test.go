package mpegts

import (
	"math"
	"testing"
)

// A stalled timestamp never ages out, so the hard cap is what bounds the tracker.
func TestBitrateSamplesAreBoundedWhenTheTimestampStalls(t *testing.T) {
	var tracker sourceBitrateTracker
	const stalled = int64(90000)

	for i := 0; i < 10*maxBitrateSamples; i++ {
		tracker.add(stalled, 1000)
	}

	if got := len(tracker.samples); got > maxBitrateSamples {
		t.Fatalf("retained %d samples, cap is %d", got, maxBitrateSamples)
	}
	if _, ok := tracker.rateKbps(); ok {
		t.Fatal("a stalled timestamp has no measurable span")
	}
}

// A large forward jump drops the stale backlog in one step instead of one per
// arrival.
func TestBitrateDropsTheBacklogAfterATimestampJump(t *testing.T) {
	var tracker sourceBitrateTracker
	for i := 0; i < 1000; i++ {
		tracker.add(int64(i)*3000, 1000)
	}
	tracker.add(90000*3600, 1000) // an hour forward

	if got := len(tracker.samples); got > 2 {
		t.Fatalf("retained %d samples after a jump, want at most 2", got)
	}
}

// The 33-bit wrap still produces a plausible rate.
func TestBitrateHandlesPTSWrap(t *testing.T) {
	var tracker sourceBitrateTracker
	start := ptsModulus - 90*3000
	for i := 0; i < 180; i++ {
		tracker.add((start+int64(i)*3000)%ptsModulus, 1000)
	}

	rate, ok := tracker.rateKbps()
	if !ok {
		t.Fatal("a wrapped stream still has a measurable window")
	}
	if math.Abs(rate-240) > 5 {
		t.Fatalf("rate %.1f kbps, want about 240 (within 2%%)", rate)
	}
}

// The normal case is unchanged: 1000 bytes every 3000 ticks is 240 kbps, and the
// retained samples stay bounded by the media-time window.
func TestBitrateNormalStreamIsUnchanged(t *testing.T) {
	var tracker sourceBitrateTracker
	for i := 0; i < 180; i++ {
		tracker.add(int64(i)*3000, 1000)
	}

	rate, ok := tracker.rateKbps()
	if !ok {
		t.Fatal("a steady stream must report a rate")
	}
	if math.Abs(rate-240) > 5 {
		t.Fatalf("rate %.1f kbps, want about 240 (within 2%%)", rate)
	}
	if got := len(tracker.samples); got > 200 {
		t.Fatalf("retained %d samples for a 6 s stream, want the window only", got)
	}
}
