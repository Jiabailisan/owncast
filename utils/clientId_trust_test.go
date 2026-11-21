package utils

import (
	"net/http/httptest"
	"testing"
)

func TestForwardedHeadersAreOnlyTrustedFromAProxyPeer(t *testing.T) {
	// Direct: a public peer cannot choose its own address through a header.
	direct := httptest.NewRequest("GET", "/", nil)
	direct.RemoteAddr = "198.51.100.7:41237"
	direct.Header.Set("CF-Connecting-IP", "203.0.113.9")
	direct.Header.Set("X-Real-IP", "203.0.113.9")
	direct.Header.Set("X-Forwarded-For", "203.0.113.9")
	if got := GetIPAddressFromRequest(direct); got != "198.51.100.7" {
		t.Fatalf("direct peer: got %q, want the peer address", got)
	}

	// Trusted proxy on loopback: Cloudflare's header is the visitor.
	proxied := httptest.NewRequest("GET", "/", nil)
	proxied.RemoteAddr = "127.0.0.1:5000"
	proxied.Header.Set("CF-Connecting-IP", "203.0.113.9")
	if got := GetIPAddressFromRequest(proxied); got != "203.0.113.9" {
		t.Fatalf("trusted proxy: got %q, want the forwarded client", got)
	}
}

func TestForwardedChainUsesTheRightmostUntrustedHop(t *testing.T) {
	request := httptest.NewRequest("GET", "/", nil)
	request.RemoteAddr = "127.0.0.1:5000"
	// The client appended its own value on the left; the proxy hops are on the right.
	request.Header.Set("X-Forwarded-For", "1.1.1.1, 203.0.113.9, 127.0.0.1")

	if got := GetIPAddressFromRequest(request); got != "203.0.113.9" {
		t.Fatalf("mixed chain: got %q, want the rightmost untrusted hop", got)
	}
}

func TestIPv4MappedAddressesAreNormalised(t *testing.T) {
	// A mapped loopback peer is still a trusted proxy, and the forwarded client is
	// reported as plain IPv4 so the limiter keys it identically.
	request := httptest.NewRequest("GET", "/", nil)
	request.RemoteAddr = "[::ffff:127.0.0.1]:5000"
	request.Header.Set("X-Real-IP", "::ffff:203.0.113.9")

	if got := GetIPAddressFromRequest(request); got != "203.0.113.9" {
		t.Fatalf("mapped proxy peer: got %q, want the normalised client", got)
	}

	// A mapped public peer is untrusted and is reported as plain IPv4.
	direct := httptest.NewRequest("GET", "/", nil)
	direct.RemoteAddr = "[::ffff:198.51.100.7]:41237"
	direct.Header.Set("X-Real-IP", "203.0.113.9")

	if got := GetIPAddressFromRequest(direct); got != "198.51.100.7" {
		t.Fatalf("mapped direct peer: got %q, want the normalised peer address", got)
	}
}
