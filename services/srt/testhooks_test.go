package srt

// Test-only hooks. Defining them in a _test.go file keeps the production build
// free of unused test seams (golangci-lint runs with tests disabled).

import "time"

// setClock replaces the time source so a test can drive the rate-limit window
// deterministically. It is safe with concurrent readers because the limiter
// reads the clock under its mutex.
func (l *authFailureLimiter) setClock(now func() time.Time) {
	if l == nil || now == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.now = now
}

// limitValue returns the configured failure limit (test assertion helper).
func (l *authFailureLimiter) limitValue() int {
	return int(l.limit.Load())
}
