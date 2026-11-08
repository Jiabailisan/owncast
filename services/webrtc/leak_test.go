package webrtc

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain fails the package when a WebRTC service, leg or session leaks a
// goroutine. Every test closes its service via t.Cleanup; goleak runs after the
// whole package.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
