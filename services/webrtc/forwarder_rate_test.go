package webrtc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSlidingRateWindowMeasuresPayloadAndResetsOnRestart(t *testing.T) {
	var win slidingRateWindow
	base := time.Unix(1_700_000_000, 0)

	// Feed 30 fps at 1000 payload bytes/frame (~240 kbps) over 6 seconds.
	for step := 0; step < 60; step++ {
		at := base.Add(time.Duration(step) * 100 * time.Millisecond)
		for f := 0; f < 3; f++ {
			win.record(at, 1000, true)
		}
	}

	now := base.Add(6 * time.Second)
	fps, kbps := win.rates(now)
	// Window spans 5s (slots > cutoffSlot): ~30 fps, ~240 kbps.
	assert.InDelta(t, 30.0, fps, 1.5)
	assert.InDelta(t, 240.0, kbps, 12.0)

	// After a restart, stale pre-restart samples must not pollute the rate.
	win.reset()
	fpsAfterReset, kbpsAfterReset := win.rates(now.Add(2 * time.Second))
	assert.Equal(t, 0.0, fpsAfterReset)
	assert.Equal(t, 0.0, kbpsAfterReset)

	// New rate (60 fps, 500 bytes/frame = 240 kbps) converges cleanly after >=1s.
	restartBase := now.Add(2 * time.Second)
	for step := 0; step < 20; step++ {
		at := restartBase.Add(time.Duration(step) * 100 * time.Millisecond)
		for f := 0; f < 6; f++ {
			win.record(at, 500, true)
		}
	}
	fpsNew, kbpsNew := win.rates(restartBase.Add(2 * time.Second))
	assert.InDelta(t, 60.0, fpsNew, 3.0)
	assert.InDelta(t, 240.0, kbpsNew, 12.0)
}
