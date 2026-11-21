package utils

import (
	"net/http/httptest"
	"testing"
)

// enableAllHeaders trusts the built-in peer set with every header enabled, which is
// what a deployment using a proxy configures explicitly.
func enableAllHeaders(t *testing.T) {
	t.Helper()
	SetTrustedProxyPolicy(nil, true, true, true)
	t.Cleanup(ResetTrustedProxyPolicy)
}

// With no header list configured nothing is trusted, even from a proxy peer.
func TestNoHeaderIsTrustedByDefault(t *testing.T) {
	ResetTrustedProxyPolicy()
	t.Cleanup(ResetTrustedProxyPolicy)

	request := httptest.NewRequest("GET", "/", nil)
	request.RemoteAddr = "127.0.0.1:5000"
	request.Header.Set("CF-Connecting-IP", "203.0.113.9")
	request.Header.Set("X-Real-IP", "203.0.113.9")
	request.Header.Set("X-Forwarded-For", "203.0.113.9")

	if got := GetIPAddressFromRequest(request); got != "127.0.0.1" {
		t.Fatalf("unconfigured policy: got %q, want the peer address", got)
	}
}

func TestForwardedHeadersAreOnlyTrustedFromAProxyPeer(t *testing.T) {
	enableAllHeaders(t)

	direct := httptest.NewRequest("GET", "/", nil)
	direct.RemoteAddr = "198.51.100.7:41237"
	direct.Header.Set("CF-Connecting-IP", "203.0.113.9")
	direct.Header.Set("X-Real-IP", "203.0.113.9")
	direct.Header.Set("X-Forwarded-For", "203.0.113.9")
	if got := GetIPAddressFromRequest(direct); got != "198.51.100.7" {
		t.Fatalf("direct peer: got %q, want the peer address", got)
	}

	proxied := httptest.NewRequest("GET", "/", nil)
	proxied.RemoteAddr = "127.0.0.1:5000"
	proxied.Header.Set("CF-Connecting-IP", "203.0.113.9")
	if got := GetIPAddressFromRequest(proxied); got != "203.0.113.9" {
		t.Fatalf("trusted proxy: got %q, want the forwarded client", got)
	}
}

func TestForwardedChainUsesTheRightmostUntrustedHop(t *testing.T) {
	enableAllHeaders(t)

	request := httptest.NewRequest("GET", "/", nil)
	request.RemoteAddr = "127.0.0.1:5000"
	request.Header.Set("X-Forwarded-For", "1.1.1.1, 203.0.113.9, 127.0.0.1")

	if got := GetIPAddressFromRequest(request); got != "203.0.113.9" {
		t.Fatalf("mixed chain: got %q, want the rightmost untrusted hop", got)
	}
}

// A chain made only of our own proxies says nothing about the client.
func TestAllTrustedHopsFallBackToThePeer(t *testing.T) {
	enableAllHeaders(t)

	request := httptest.NewRequest("GET", "/", nil)
	request.RemoteAddr = "127.0.0.1:5000"
	request.Header.Set("X-Forwarded-For", "127.0.0.1, 127.0.0.1")

	if got := GetIPAddressFromRequest(request); got != "127.0.0.1" {
		t.Fatalf("all-trusted chain: got %q, want the peer address", got)
	}
}

func TestIPv4MappedAddressesAreNormalised(t *testing.T) {
	enableAllHeaders(t)

	request := httptest.NewRequest("GET", "/", nil)
	request.RemoteAddr = "[::ffff:127.0.0.1]:5000"
	request.Header.Set("X-Real-IP", "::ffff:203.0.113.9")
	if got := GetIPAddressFromRequest(request); got != "203.0.113.9" {
		t.Fatalf("mapped proxy peer: got %q, want the normalised client", got)
	}

	direct := httptest.NewRequest("GET", "/", nil)
	direct.RemoteAddr = "[::ffff:198.51.100.7]:41237"
	direct.Header.Set("X-Real-IP", "203.0.113.9")
	if got := GetIPAddressFromRequest(direct); got != "198.51.100.7" {
		t.Fatalf("mapped direct peer: got %q, want the normalised peer address", got)
	}
}

func TestExplicitTrustedRangesReplaceTheDefault(t *testing.T) {
	t.Cleanup(ResetTrustedProxyPolicy)
	SetTrustedProxyPolicy([]string{"10.0.0.0/8"}, true, true, true)

	inside := httptest.NewRequest("GET", "/", nil)
	inside.RemoteAddr = "10.1.2.3:5000"
	inside.Header.Set("X-Real-IP", "203.0.113.9")
	if got := GetIPAddressFromRequest(inside); got != "203.0.113.9" {
		t.Fatalf("trusted range: got %q, want the forwarded client", got)
	}

	outside := httptest.NewRequest("GET", "/", nil)
	outside.RemoteAddr = "127.0.0.1:5000"
	outside.Header.Set("X-Real-IP", "203.0.113.9")
	if got := GetIPAddressFromRequest(outside); got != "127.0.0.1" {
		t.Fatalf("unlisted peer: got %q, want the peer address", got)
	}
}

// A range list that does not parse disables trust instead of falling back to the
// built-in set.
func TestInvalidRangesDisableTrust(t *testing.T) {
	t.Cleanup(ResetTrustedProxyPolicy)
	SetTrustedProxyPolicy([]string{"not-a-cidr"}, true, true, true)

	request := httptest.NewRequest("GET", "/", nil)
	request.RemoteAddr = "127.0.0.1:5000"
	request.Header.Set("CF-Connecting-IP", "203.0.113.9")

	if got := GetIPAddressFromRequest(request); got != "127.0.0.1" {
		t.Fatalf("invalid ranges: got %q, want the peer address", got)
	}
}

func TestPartiallyInvalidRangesDisableTrust(t *testing.T) {
	t.Cleanup(ResetTrustedProxyPolicy)
	SetTrustedProxyPolicy([]string{"10.0.0.0/8", "203.0.113.0/33"}, true, true, true)

	request := httptest.NewRequest("GET", "/", nil)
	request.RemoteAddr = "10.1.2.3:5000"
	request.Header.Set("X-Real-IP", "203.0.113.9")

	if got := GetIPAddressFromRequest(request); got != "10.1.2.3" {
		t.Fatalf("partially invalid ranges: got %q, want the peer address", got)
	}
}

func TestDisabledHeaderIsIgnored(t *testing.T) {
	t.Cleanup(ResetTrustedProxyPolicy)
	SetTrustedProxyPolicy(nil, false, true, true)

	request := httptest.NewRequest("GET", "/", nil)
	request.RemoteAddr = "127.0.0.1:5000"
	request.Header.Set("CF-Connecting-IP", "203.0.113.9")
	request.Header.Set("X-Real-IP", "198.51.100.4")

	if got := GetIPAddressFromRequest(request); got != "198.51.100.4" {
		t.Fatalf("disabled header: got %q, want the next enabled source", got)
	}
}

func TestForgedHopIsIgnoredInFavourOfTheProxyAppendedOne(t *testing.T) {
	enableAllHeaders(t)

	request := httptest.NewRequest("GET", "/", nil)
	request.RemoteAddr = "127.0.0.1:5000"
	request.Header.Set("X-Forwarded-For", "1.1.1.1, 203.0.113.9")

	if got := GetIPAddressFromRequest(request); got != "203.0.113.9" {
		t.Fatalf("forged chain: got %q, want the proxy-appended client", got)
	}
}
