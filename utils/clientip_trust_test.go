package utils

import (
	"net/http/httptest"
	"testing"
)

// The forwarding headers are only trusted when the peer is a proxy on this host,
// because that is the only case where the header cannot have been written by the
// client. These cases pin that contract: the earlier tests used a public peer and
// therefore never exercised the header path at all.
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
			name:     "loopback proxy uses the original client hop",
			peer:     "127.0.0.1:5000",
			headers:  map[string]string{"X-Forwarded-For": "1.2.3.4, 198.51.100.7"},
			expected: "1.2.3.4",
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
			name:     "lan peer is not a trusted proxy",
			peer:     "192.168.50.7:5000",
			headers:  map[string]string{"X-Forwarded-For": "198.51.100.7"},
			expected: "192.168.50.7",
		},
		{
			name:     "invalid cloudflare value falls back to the chain",
			peer:     "127.0.0.1:5000",
			headers:  map[string]string{"X-Forwarded-For": "1.2.3.4, 198.51.100.11", "CF-Connecting-IP": "not-an-ip"},
			expected: "1.2.3.4",
		},
		{
			name:     "unparsable first hop falls back to the last one",
			peer:     "127.0.0.1:5000",
			headers:  map[string]string{"X-Forwarded-For": "unknown, 198.51.100.12"},
			expected: "198.51.100.12",
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
