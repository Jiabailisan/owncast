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

// An explicit range list replaces the built-in peer check, so a deployment can state
// exactly which proxies may supply headers.
func TestExplicitTrustedRangesReplaceTheDefault(t *testing.T) {
	t.Cleanup(func() { SetTrustedProxyPolicy(nil, true, true, true) })
	SetTrustedProxyPolicy([]string{"10.0.0.0/8"}, true, true, true)

	inside := httptest.NewRequest("GET", "/", nil)
	inside.RemoteAddr = "10.1.2.3:5000"
	inside.Header.Set("X-Real-IP", "203.0.113.9")
	if got := GetIPAddressFromRequest(inside); got != "203.0.113.9" {
		t.Fatalf("trusted range: got %q, want the forwarded client", got)
	}

	// Loopback is no longer trusted once the ranges are explicit.
	outside := httptest.NewRequest("GET", "/", nil)
	outside.RemoteAddr = "127.0.0.1:5000"
	outside.Header.Set("X-Real-IP", "203.0.113.9")
	if got := GetIPAddressFromRequest(outside); got != "127.0.0.1" {
		t.Fatalf("unlisted peer: got %q, want the peer address", got)
	}
}

// A header the policy disables is ignored even from a trusted peer.
func TestDisabledHeaderIsIgnored(t *testing.T) {
	t.Cleanup(func() { SetTrustedProxyPolicy(nil, true, true, true) })
	SetTrustedProxyPolicy(nil, false, true, true)

	request := httptest.NewRequest("GET", "/", nil)
	request.RemoteAddr = "127.0.0.1:5000"
	request.Header.Set("CF-Connecting-IP", "203.0.113.9")
	request.Header.Set("X-Real-IP", "198.51.100.4")

	if got := GetIPAddressFromRequest(request); got != "198.51.100.4" {
		t.Fatalf("disabled header: got %q, want the next enabled source", got)
	}
}

// A client that forges a hop cannot win: the proxy appends the address it actually
// saw, so the rightmost untrusted hop is the real client.
func TestForgedHopIsIgnoredInFavourOfTheProxyAppendedOne(t *testing.T) {
	request := httptest.NewRequest("GET", "/", nil)
	request.RemoteAddr = "127.0.0.1:5000"
	// The client claimed 1.1.1.1; our proxy appended the address it saw.
	request.Header.Set("X-Forwarded-For", "1.1.1.1, 203.0.113.9")

	if got := GetIPAddressFromRequest(request); got != "203.0.113.9" {
		t.Fatalf("forged chain: got %q, want the proxy-appended client", got)
	}
}

// When every hop is one of our own proxies the leftmost known address is reported
// rather than nothing.
func TestAllTrustedHopsFallBackToTheLeftmost(t *testing.T) {
	request := httptest.NewRequest("GET", "/", nil)
	request.RemoteAddr = "127.0.0.1:5000"
	request.Header.Set("X-Forwarded-For", "127.0.0.1, 127.0.0.1")

	if got := GetIPAddressFromRequest(request); got != "127.0.0.1" {
		t.Fatalf("all-trusted chain: got %q, want the leftmost known address", got)
	}
}
