package webrtc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSignalingLimiterBurstThenRefill(t *testing.T) {
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	limiter := newSignalingLimiter(60, clock) // 60/min => 1/s, burst 20

	allowed := 0
	for i := 0; i < 25; i++ {
		if limiter.allow("203.0.113.1") {
			allowed++
		}
	}
	assert.Equal(t, 20, allowed, "burst should be limit/3")

	// One second later exactly one token has been refilled.
	now = now.Add(time.Second)
	assert.True(t, limiter.allow("203.0.113.1"))
	assert.False(t, limiter.allow("203.0.113.1"))
}

func TestSignalingLimiterSeparateBucketsPerIP(t *testing.T) {
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	limiter := newSignalingLimiter(3, clock) // burst 1

	assert.True(t, limiter.allow("203.0.113.2"))
	assert.False(t, limiter.allow("203.0.113.2"))
	assert.True(t, limiter.allow("203.0.113.3"))
}

func TestSignalingLimiterResetOnLimitChange(t *testing.T) {
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	limiter := newSignalingLimiter(3, clock)

	assert.True(t, limiter.allow("203.0.113.4"))
	assert.False(t, limiter.allow("203.0.113.4"))

	limiter.setLimit(300)
	assert.True(t, limiter.allow("203.0.113.4"))
}

func TestSignalingLimiterGarbageCollectsBuckets(t *testing.T) {
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	limiter := newSignalingLimiter(60, clock)

	limiter.allow("198.51.100.1")
	now = now.Add(limiterIdleTTL + time.Minute)
	limiter.sweep()

	limiter.mu.Lock()
	count := len(limiter.buckets)
	limiter.mu.Unlock()
	assert.Equal(t, 0, count)
}
