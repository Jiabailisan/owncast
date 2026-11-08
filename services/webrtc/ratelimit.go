package webrtc

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const limiterIdleTTL = 10 * time.Minute

type limiterBucket struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// signalingLimiter is a per-IP token bucket applied to WHEP POST requests.
// PATCH/DELETE are session-bound and deliberately not limited: a bandwidth
// constrained viewer may need many trickle requests, and they cannot create
// new sessions.
type signalingLimiter struct {
	mu      sync.Mutex
	limit   int
	burst   int
	buckets map[string]*limiterBucket
	now     func() time.Time
	lastGC  time.Time
}

func newSignalingLimiter(perMinute int, now func() time.Time) *signalingLimiter {
	if now == nil {
		now = time.Now
	}
	l := &signalingLimiter{
		buckets: map[string]*limiterBucket{},
		now:     now,
	}
	l.setLimitLocked(perMinute)

	return l
}

func (l *signalingLimiter) setLimit(perMinute int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.setLimitLocked(perMinute)
}

func (l *signalingLimiter) setLimitLocked(perMinute int) {
	if perMinute <= 0 {
		perMinute = defaultRateLimit
	}
	l.limit = perMinute
	l.burst = max(1, perMinute/3)
	// Re-create buckets lazily with the new rate; existing buckets keep their
	// old token state until they are next used, which is acceptable for a
	// rate limit (it only ever becomes more permissive).
	l.buckets = map[string]*limiterBucket{}
}

func (l *signalingLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	if now.Sub(l.lastGC) > time.Minute {
		l.gcLocked(now)
	}

	bucket, ok := l.buckets[ip]
	if !ok {
		perSecond := rate.Limit(float64(l.limit) / 60.0)
		bucket = &limiterBucket{
			limiter:  rate.NewLimiter(perSecond, l.burst),
			lastSeen: now,
		}
		l.buckets[ip] = bucket
	}
	bucket.lastSeen = now

	return bucket.limiter.AllowN(now, 1)
}

func (l *signalingLimiter) gcLocked(now time.Time) {
	l.lastGC = now
	for ip, bucket := range l.buckets {
		if now.Sub(bucket.lastSeen) > limiterIdleTTL {
			delete(l.buckets, ip)
		}
	}
}

func (l *signalingLimiter) sweep() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.gcLocked(l.now())
}
