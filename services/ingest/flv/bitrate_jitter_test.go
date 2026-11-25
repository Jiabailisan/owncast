package flv

import "testing"

func TestFLVRateTrackerToleratesMildNonMonotonicDTSJitter(t *testing.T) {
	var tracker rateTracker

	// Feed 2 seconds of video tags (~1000 bytes every 100ms -> ~80 kbps),
	// with a 20ms backward DTS jitter at 1500ms -> 1480ms.
	timestamps := []uint32{
		0, 100, 200, 300, 400, 500, 600, 700, 800, 900, 1000,
		1100, 1200, 1300, 1400, 1500, 1480, 1600, 1700, 1800, 1900, 2000,
	}
	for _, ts := range timestamps {
		tracker.add(ts, 1000)
	}

	if !tracker.valid {
		t.Fatal("rateTracker became invalid after mild 20ms backward DTS jitter")
	}
	if len(tracker.points) < 15 {
		t.Fatalf("window was unexpectedly evicted on mild jitter: only %d points kept", len(tracker.points))
	}
	if tracker.value < 70 || tracker.value > 110 {
		t.Fatalf("bitrate = %.2f kbps, expected ~88 kbps", tracker.value)
	}
}
