package srt

import (
	"io"
	"net/netip"
	"testing"
)

// A throttled peer must not reach stream ID parsing or passphrase verification, and
// must not move the failure counters: those are reserved for real attempts.
func TestRateLimitedRequestSkipsParsingAndPassphrase(t *testing.T) {
	cfg := testAuthConfig()
	cfg.AuthFailureRateLimit = 2
	svc := newAuthTestService(t, &cfg)

	ip := netip.MustParseAddr("127.0.0.1")
	for i := 0; i < 2; i++ {
		request := &fakeConnRequest{
			remote:   testRemoteAddr(t, "127.0.0.1:7200"),
			version:  5,
			streamID: "wrongkey",
		}
		if _, ok := svc.authenticate(request); ok {
			t.Fatal("expected a bad key rejection")
		}
	}

	svc.limits.mu.Lock()
	before := svc.limits.entries[ip].count
	svc.limits.mu.Unlock()

	// Encrypted with a malformed stream ID: both checks would run if the rate limit
	// still sat behind them.
	request := &fakeConnRequest{
		remote:    testRemoteAddr(t, "127.0.0.1:7201"),
		version:   5,
		streamID:  "!!not-a-valid-stream-id",
		encrypted: true,
	}
	if _, ok := svc.authenticate(request); ok {
		t.Fatal("a rate limited request must not be accepted")
	}
	if !request.rejected {
		t.Fatal("a rate limited request must be rejected")
	}
	if request.passphrase != "" {
		t.Fatal("a rate limited request must not reach passphrase verification")
	}

	svc.limits.mu.Lock()
	after := svc.limits.entries[ip].count
	svc.limits.mu.Unlock()
	if after != before {
		t.Fatalf("the failure counter moved from %d to %d: stream ID parsing ran", before, after)
	}
}

// A request under the limit still goes through the normal path.
func TestRateLimitDoesNotBlockAValidRequest(t *testing.T) {
	cfg := testAuthConfig()
	cfg.AuthFailureRateLimit = 2
	svc := newAuthTestService(t, &cfg)

	request := &fakeConnRequest{
		remote:   testRemoteAddr(t, "127.0.0.1:7300"),
		version:  5,
		streamID: "testkey",
		conn:     &fakeConn{readErr: io.EOF},
	}

	sess, ok := svc.authenticate(request)
	if !ok {
		t.Fatal("a valid request must still be accepted")
	}
	defer sess.Close(ErrRemoteClosed)
}
