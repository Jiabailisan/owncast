package utils

import (
	"net/http/httptest"
	"testing"
)

// The forwarding headers are only trusted when the peer is a proxy, because that
// is the only case where the header cannot have been written by the client. These
// cases pin that contract, including the forgery case the earlier tests missed.
func TestGetIPAddressFromRequestTrustsHeadersOnlyFromLoopback(t *testing.T) {
	cases := []struct {
		name     string
		peer     string
		headers  map[string]string
		expected string
	}{
		{
			name:     "public peer ignores forwarded header",
			peer:     "203.0.113.195:41237",
			headers:  map[string]string{"X-Forwarded-For": "198.51.100.7, 203.0.113.195"},
			expected: "203.0.113.195",
		},
		{
			name:     "proxy chain reports the client, not the proxy hops",
			peer:     "127.0.0.1:5000",
			headers:  map[string]string{"X-Forwarded-For": "1.2.3.4, 198.51.100.7"},
			expected: "198.51.100.7",
		},
		{
			name:     "forged hop at the head of the chain is ignored",
			peer:     "127.0.0.1:5000",
			headers:  map[string]string{"X-Forwarded-For": "1.2.3.4, 203.0.113.90"},
			expected: "203.0.113.90",
		},
		{
			name:     "loopback proxy prefers cloudflare client address",
			peer:     "127.0.0.1:5000",
			headers:  map[string]string{"X-Forwarded-For": "1.2.3.4", "CF-Connecting-IP": "198.51.100.9"},
			expected: "198.51.100.9",
		},
		{
			name:     "loopback proxy uses x-real-ip when there is no chain",
			peer:     "127.0.0.1:5000",
			headers:  map[string]string{"X-Real-IP": "198.51.100.21"},
			expected: "198.51.100.21",
		},
		{
			name:     "another machine on the network is not a trusted proxy",
			peer:     "192.0.2.55:5000",
			headers:  map[string]string{"X-Forwarded-For": "198.51.100.7"},
			expected: "192.0.2.55",
		},
		{
			name:     "invalid cloudflare value falls back to the chain",
			peer:     "127.0.0.1:5000",
			headers:  map[string]string{"X-Forwarded-For": "1.2.3.4, 198.51.100.11", "CF-Connecting-IP": "not-an-ip"},
			expected: "198.51.100.11",
		},
		{
			name:     "unparsable hop is skipped",
			peer:     "127.0.0.1:5000",
			headers:  map[string]string{"X-Forwarded-For": "unknown, 198.51.100.12"},
			expected: "198.51.100.12",
		},
		{
			name:     "chain of only our proxies reports the leftmost address",
			peer:     "127.0.0.1:5000",
			headers:  map[string]string{"X-Forwarded-For": "127.0.0.1, 127.0.0.2"},
			expected: "127.0.0.1",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest("GET", "/", nil)
			request.RemoteAddr = testCase.peer
			for key, value := range testCase.headers {
				request.Header.Set(key, value)
			}

			if got := GetIPAddressFromRequest(request); got != testCase.expected {
				t.Fatalf("expected %s, got %s", testCase.expected, got)
			}
		})
	}
}

// TestIsTrustedProxyPeer pins which peers may write forwarding headers. The host's
// own addresses count because a proxy on this machine reaches Owncast from them,
// which is how Lucky is deployed; an arbitrary LAN address does not.
func TestIsTrustedProxyPeer(t *testing.T) {
	if !isTrustedProxyPeer("127.0.0.1") {
		t.Fatal("loopback must be a trusted proxy")
	}
	if isTrustedProxyPeer("203.0.113.195") {
		t.Fatal("a public peer must not be a trusted proxy")
	}
	if isTrustedProxyPeer("not-an-ip") {
		t.Fatal("an unparsable address must not be a trusted proxy")
	}
}
