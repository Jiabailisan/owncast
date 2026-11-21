package utils

import (
	"crypto/md5" //nolint
	"encoding/hex"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
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
//  2. The rightmost X-Forwarded-For hop that is not one of our own proxies. Walking
//     from the right stops a client from choosing its own address: anything it
//     appended sits to the left of the hops our proxies added.
//  3. The peer address.
//
// Headers are only trusted when the peer is loopback/private, i.e. when a proxy
// is actually in front. A request arriving straight from the internet on a public
// peer keeps its real address, so the headers cannot be used to forge it.
func GetIPAddressFromRequest(req *http.Request) string {
	peer := NormalizeIP(hostOnly(req.RemoteAddr))
	policy := TrustedProxyPolicy()
	if peerIsTrusted(peer) {
		if cloudflare := strings.TrimSpace(req.Header.Get("CF-Connecting-IP")); policy.cloudflare && cloudflare != "" {
			if net.ParseIP(cloudflare) != nil {
				return NormalizeIP(cloudflare)
			}
		}
		// X-Real-IP is set by several proxies and, unlike the X-Forwarded-For
		// chain, cannot carry a client-supplied list.
		if realIP := strings.TrimSpace(req.Header.Get("X-Real-IP")); policy.realIP && realIP != "" {
			if net.ParseIP(realIP) != nil {
				return NormalizeIP(realIP)
			}
		}

		if forwarded := req.Header.Get("X-Forwarded-For"); policy.forwardedFor && forwarded != "" {
			if hop := clientHopFromChain(forwarded); hop != "" {
				return hop
			}
		}
	}

	return peer
}

// NormalizeIP returns the canonical string form of an address, so the same client is
// keyed identically whichever textual form its proxy used: IPv4-mapped IPv6 becomes
// plain IPv4, which keeps the WHEP rate limit and capacity counters from splitting
// one client across two keys. Invalid input is returned unchanged.
func NormalizeIP(address string) string {
	addr, err := netip.ParseAddr(strings.TrimSpace(address))
	if err != nil {
		return address
	}

	return addr.Unmap().String()
}

// clientHopFromChain walks X-Forwarded-For from right to left and returns the
// first hop that is not one of our own proxies. Walking from the left instead
// returns whatever the client put in the header: with the usual
// $proxy_add_x_forwarded_for configuration a client-supplied value stays at the
// head of the list, so any viewer could claim any address (and dodge an IP ban).
func clientHopFromChain(forwarded string) string {
	hops := strings.Split(forwarded, ",")

	for index := len(hops) - 1; index >= 0; index-- {
		hop := strings.TrimSpace(hops[index])
		if net.ParseIP(hop) == nil {
			continue
		}

		// Skip the proxies we already know; the first address past them is the
		// client that reached the outermost proxy.
		if peerIsTrusted(hop) {
			continue
		}

		return NormalizeIP(hop)
	}

	// Every hop was one of our own proxies, so the chain says nothing about the
	// client. The caller falls back to the connection peer rather than to the
	// leftmost value, which is the one a client controls.
	return ""
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

	// A proxy running on this host is trusted to have written the forwarding
	// headers. That covers loopback and the host's own addresses: a proxy bound
	// to the LAN interface (Lucky on the NAS) reaches Owncast from the machine's
	// own LAN address, and treating that as an untrusted LAN client is what made
	// every tunnelled viewer look like the proxy itself.
	if ip.IsLoopback() {
		return true
	}

	return isLocalHostAddress(ip)
}

// localHostAddresses caches the machine's own addresses; the interface list only
// changes when the network does, and a stale entry only costs a header trust.
var (
	localAddressesOnce sync.Once
	localAddresses     = map[string]struct{}{}
)

// isLocalHostAddress reports whether the address belongs to this machine.
func isLocalHostAddress(ip net.IP) bool {
	localAddressesOnce.Do(func() {
		addresses, err := net.InterfaceAddrs()
		if err != nil {
			return
		}

		for _, address := range addresses {
			var host string
			switch value := address.(type) {
			case *net.IPNet:
				host = value.IP.String()
			case *net.IPAddr:
				host = value.IP.String()
			}

			if host != "" {
				localAddresses[host] = struct{}{}
			}
		}
	})

	_, ok := localAddresses[ip.String()]

	return ok
}

// isIPv6 reports whether the address looks like an IPv6 address.
//
//nolint:unused // exercised by the address parsing tests
func isIPv6(address string) bool {
	return strings.Count(address, ":") >= 2
}
