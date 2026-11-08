package srt

import (
	"context"
	"net/netip"
	"sync"
	"testing"
	"time"
)

// fakeClock is a mutex-protected time source so it can be shared between a
// test goroutine (Advance) and service goroutines (Now) under -race.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newTestLimiter(limit int) (*authFailureLimiter, *fakeClock) {
	clock := &fakeClock{now: time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)}
	limiter := newAuthFailureLimiter(limit)
	limiter.now = clock.Now
	return limiter, clock
}

func TestAuthFailureLimiterAllowsUnderLimit(t *testing.T) {
	limiter, _ := newTestLimiter(2)
	ip := netip.MustParseAddr("203.0.113.7")

	if !limiter.Allow(ip) {
		t.Fatal("expected a fresh IP to be allowed")
	}
	limiter.RecordFailure(ip)
	limiter.RecordFailure(ip)
	if limiter.Allow(ip) {
		t.Fatal("expected the IP to be limited after reaching the limit")
	}
}

func TestAuthFailureLimiterWindowExpires(t *testing.T) {
	limiter, clock := newTestLimiter(1)
	ip := netip.MustParseAddr("203.0.113.7")

	limiter.RecordFailure(ip)
	if limiter.Allow(ip) {
		t.Fatal("expected the IP to be limited inside the window")
	}

	clock.Advance(authFailureWindow + time.Second)
	if !limiter.Allow(ip) {
		t.Fatal("expected the limit to expire after the window")
	}

	limiter.RecordFailure(ip)
	if limiter.Allow(ip) {
		t.Fatal("expected a new failure to start a new window")
	}
}

func TestAuthFailureLimiterZeroDisables(t *testing.T) {
	limiter, _ := newTestLimiter(0)
	ip := netip.MustParseAddr("203.0.113.7")

	for i := 0; i < 100; i++ {
		limiter.RecordFailure(ip)
	}
	if !limiter.Allow(ip) {
		t.Fatal("a zero limit must disable limiting")
	}
	if limiter.Len() != 0 {
		t.Fatalf("a disabled limiter should not track entries, got %d", limiter.Len())
	}
}

func TestAuthFailureLimiterSweepAndBound(t *testing.T) {
	limiter, clock := newTestLimiter(1)
	limiter.maxTracked = 2

	for _, address := range []string{"203.0.113.1", "203.0.113.2", "203.0.113.3"} {
		limiter.RecordFailure(netip.MustParseAddr(address))
	}
	if limiter.Len() > 2 {
		t.Fatalf("limiter tracked %d entries, expected at most 2", limiter.Len())
	}

	clock.Advance(authFailureWindow + time.Second)
	limiter.Sweep()
	if limiter.Len() != 0 {
		t.Fatalf("sweep did not clear expired entries, got %d", limiter.Len())
	}
}

func TestAuthFailureLimiterSetLimit(t *testing.T) {
	limiter, _ := newTestLimiter(5)
	ip := netip.MustParseAddr("203.0.113.7")
	limiter.RecordFailure(ip)
	limiter.SetLimit(1)
	if limiter.Allow(ip) {
		t.Fatal("expected the new limit to apply to recorded failures")
	}
}

func TestAuthFailureLimiterSweeperStops(t *testing.T) {
	limiter, _ := newTestLimiter(1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		limiter.RunSweeper(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("sweeper did not stop after context cancellation")
	}
}

func TestPendingCounter(t *testing.T) {
	counter := newPendingCounter(2)

	if counter.InFlight() != 0 {
		t.Fatalf("initial in-flight = %d, want 0", counter.InFlight())
	}
	if !counter.TryAcquire() || !counter.TryAcquire() {
		t.Fatal("expected two acquisitions to succeed")
	}
	if counter.TryAcquire() {
		t.Fatal("expected the third acquisition to fail")
	}
	counter.Release()
	if !counter.TryAcquire() {
		t.Fatal("expected an acquisition after release to succeed")
	}
	if counter.InFlight() != 2 {
		t.Fatalf("in-flight = %d, want 2", counter.InFlight())
	}

	counter.SetLimit(1)
	counter.Release()
	counter.Release()
	if !counter.TryAcquire() {
		t.Fatal("expected an acquisition under the lowered limit to succeed")
	}
	if counter.TryAcquire() {
		t.Fatal("expected the lowered limit to block a second acquisition")
	}
	counter.Release()
}

func TestPendingCounterZeroMeansUnlimited(t *testing.T) {
	counter := newPendingCounter(0)
	for i := 0; i < 100; i++ {
		if !counter.TryAcquire() {
			t.Fatal("a zero limit must not block acquisitions")
		}
	}
}
