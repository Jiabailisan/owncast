package srt

import (
	"context"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"
)

// authFailureWindow is the sliding window used for per-IP authentication
// failure limiting.
const authFailureWindow = time.Minute

// maxTrackedIPs bounds the memory consumed by the failure limiter under a
// spoofed-source flood.
const maxTrackedIPs = 10000

// authFailureLimiter counts authentication failures per peer IP inside a
// fixed one-minute window. A limit of zero disables limiting.
type authFailureLimiter struct {
	mu         sync.Mutex
	limit      atomic.Int64
	entries    map[netip.Addr]*failureEntry
	now        func() time.Time
	window     time.Duration
	maxTracked int
}

type failureEntry struct {
	count       int
	windowStart time.Time
	lastSeen    time.Time
}

func newAuthFailureLimiter(limit int) *authFailureLimiter {
	l := &authFailureLimiter{
		entries:    make(map[netip.Addr]*failureEntry),
		now:        time.Now,
		window:     authFailureWindow,
		maxTracked: maxTrackedIPs,
	}
	l.limit.Store(int64(limit))
	return l
}

// SetLimit changes the limit without dropping the recorded failures. It is
// safe to call while read requests are in flight.
func (l *authFailureLimiter) SetLimit(limit int) {
	l.limit.Store(int64(limit))
}

// Allow reports whether the IP may attempt authentication right now. A nil
// limiter or a non-positive limit allows everything.
func (l *authFailureLimiter) Allow(addr netip.Addr) bool {
	if l == nil {
		return true
	}
	limit := int(l.limit.Load())
	if limit <= 0 {
		return true
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	entry, ok := l.entries[addr.Unmap()]
	if !ok {
		return true
	}
	if l.now().Sub(entry.windowStart) >= l.window {
		return true
	}
	return entry.count < limit
}

// RecordFailure records one authentication failure for the IP.
func (l *authFailureLimiter) RecordFailure(addr netip.Addr) {
	if l == nil {
		return
	}
	limit := int(l.limit.Load())
	if limit <= 0 {
		return
	}

	addr = addr.Unmap()

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()

	if len(l.entries) >= l.maxTracked {
		l.evictOldestLocked()
	}

	entry, ok := l.entries[addr]
	if !ok {
		l.entries[addr] = &failureEntry{count: 1, windowStart: now, lastSeen: now}
		return
	}
	if now.Sub(entry.windowStart) >= l.window {
		entry.count = 1
		entry.windowStart = now
	} else {
		entry.count++
	}
	entry.lastSeen = now
}

// Sweep drops expired entries. It is called periodically by the service.
func (l *authFailureLimiter) Sweep() {
	if l == nil {
		return
	}
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	for addr, entry := range l.entries {
		if now.Sub(entry.lastSeen) >= l.window {
			delete(l.entries, addr)
		}
	}
}

// RunSweeper periodically drops expired entries until ctx is canceled.
func (l *authFailureLimiter) RunSweeper(ctx context.Context) {
	if l == nil {
		return
	}
	ticker := time.NewTicker(l.window)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.Sweep()
		}
	}
}

// Len returns the number of tracked addresses (for tests and diagnostics).
func (l *authFailureLimiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}

func (l *authFailureLimiter) evictOldestLocked() {
	var oldestAddr netip.Addr
	var oldestTime time.Time
	first := true
	for addr, entry := range l.entries {
		if first || entry.lastSeen.Before(oldestTime) {
			oldestAddr = addr
			oldestTime = entry.lastSeen
			first = false
		}
	}
	if !first {
		delete(l.entries, oldestAddr)
	}
}

// pendingCounter bounds the number of unauthenticated connection requests that
// are being processed concurrently. It is a defense-in-depth cap on the
// application-side unauthenticated window (gosrt has no handshake timeout
// hook). A limit of zero means unlimited.
type pendingCounter struct {
	inflight atomic.Int64
	limit    atomic.Int64
}

func newPendingCounter(limit int) *pendingCounter {
	p := &pendingCounter{}
	p.limit.Store(int64(limit))
	return p
}

// SetLimit changes the cap without dropping in-flight requests.
func (p *pendingCounter) SetLimit(limit int) {
	p.limit.Store(int64(limit))
}

// TryAcquire reserves one pending slot.
func (p *pendingCounter) TryAcquire() bool {
	if p == nil {
		return true
	}
	limit := p.limit.Load()
	if limit <= 0 {
		p.inflight.Add(1)
		return true
	}
	for {
		current := p.inflight.Load()
		if current >= limit {
			return false
		}
		if p.inflight.CompareAndSwap(current, current+1) {
			return true
		}
	}
}

// Release frees one pending slot.
func (p *pendingCounter) Release() {
	if p == nil {
		return
	}
	p.inflight.Add(-1)
}

// InFlight returns the number of pending requests (for tests and snapshots).
func (p *pendingCounter) InFlight() int64 {
	if p == nil {
		return 0
	}
	return p.inflight.Load()
}
