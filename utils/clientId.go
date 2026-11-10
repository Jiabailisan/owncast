package utils

import (
	"crypto/md5" //nolint
	"encoding/hex"
	"net"
	"net/http"
	"strings"
)

// GenerateClientIDFromRequest generates a client id from the provided request.
func GenerateClientIDFromRequest(req *http.Request) string {
	ipAddress := GetIPAddressFromRequest(req)
	clientID := ipAddress + req.UserAgent()

	// Create a MD5 hash of this ip + useragent
	b := md5.Sum([]byte(clientID)) // nolint
	return hex.EncodeToString(b[:])
}

// GetIPAddressFromRequest returns the IP address from a http request.
//
// When a proxy is in front (Lucky, or a Cloudflare Tunnel where the peer is
// cloudflared on loopback) the peer address is the proxy, so the client address
// has to come from a header:
//
//  1. CF-Connecting-IP - set by Cloudflare, which strips any client value, so it
//     is the real visitor on the tunnel path.
//  2. The LAST X-Forwarded-For hop - appended by the nearest proxy. Taking the
//     last hop instead of the first stops a client from choosing its own address.
//  3. The peer address.
//
// Headers are only trusted when the peer is loopback/private, i.e. when a proxy
// is actually in front. A request arriving straight from the internet on a public
// peer keeps its real address, so the headers cannot be used to forge it.
func GetIPAddressFromRequest(req *http.Request) string {
	peer := hostOnly(req.RemoteAddr)
	if isTrustedProxyPeer(peer) {
		if cloudflare := strings.TrimSpace(req.Header.Get("CF-Connecting-IP")); cloudflare != "" {
			if net.ParseIP(cloudflare) != nil {
				return cloudflare
			}
		}
		if forwarded := req.Header.Get("X-Forwarded-For"); forwarded != "" {
			hops := strings.Split(forwarded, ",")
			last := strings.TrimSpace(hops[len(hops)-1])
			if net.ParseIP(last) != nil {
				return last
			}
		}
	}

	return peer
}

// hostOnly strips the port from a RemoteAddr value.
func hostOnly(address string) string {
	if host, _, err := net.SplitHostPort(address); err == nil {
		return host
	}

	return address
}

// isTrustedProxyPeer reports whether the peer is a local address, which means a
// reverse proxy or tunnel connector forwarded this request.
func isTrustedProxyPeer(address string) bool {
	ip := net.ParseIP(address)
	if ip == nil {
		return false
	}

	// Only a proxy running on this host (Lucky, nginx, caddy bound to
	// 127.0.0.1) is trusted to have written the forwarding headers. A LAN peer
	// talking to Owncast directly must keep its own address, otherwise any LAN
	// client could claim to be someone else.
	return ip.IsLoopback()
}

// isIPv6 reports whether the address looks like an IPv6 address.
//
//nolint:unused // exercised by the address parsing tests
func isIPv6(address string) bool {
	return strings.Count(address, ":") >= 2
}
