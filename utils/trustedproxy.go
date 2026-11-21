package utils

import (
	"net/netip"
	"os"
	"strings"
	"sync/atomic"
)

// Which forwarding headers may be trusted, and from which peers.
//
// The rule the code implements, and the deployment it matches:
//
//   - A forwarding header is only read when the TCP peer is a proxy we run. By
//     default that is loopback plus this host's own addresses, which covers both
//     deployment shapes: cloudflared dials Owncast on loopback, and the Lucky
//     reverse proxy on the NAS reaches Owncast from the NAS's own LAN address.
//   - CF-Connecting-IP is trusted only from that proxy, and only because Cloudflare
//     overwrites the header on every request, so a client value cannot survive. It
//     is listed first: on the tunnel path it is the visitor.
//   - X-Real-IP is trusted from the same proxy. A proxy that passes a client-supplied
//     X-Real-IP through unchanged would defeat this, so it must be configured to set
//     it, not forward it.
//   - X-Forwarded-For is walked from the right, skipping only hops inside the trusted
//     ranges. A client-supplied value sits to the left of the hops our proxies added,
//     so it is never returned.
//   - With no trusted peer the peer address is used, so a direct connection cannot
//     claim another address.
//
// Two environment variables make the policy explicit for a deployment:
//
//	OWNCAST_TRUSTED_PROXY_RANGES=10.0.0.0/8,192.168.0.0/16,127.0.0.1/32
//	    Replaces the default peer set. Leave unset to keep loopback plus this host's
//	    own addresses, which is what the shipped deployments rely on.
//	OWNCAST_TRUST_PROXY_HEADERS=cf-connecting-ip,x-real-ip,x-forwarded-for
//	    The headers a trusted peer may supply. Defaults to all three.
const (
	envTrustedProxyRanges = "OWNCAST_TRUSTED_PROXY_RANGES"
	envTrustProxyHeaders  = "OWNCAST_TRUST_PROXY_HEADERS"

	headerCloudflare = "cf-connecting-ip"
	headerRealIP     = "x-real-ip"
	headerForwarded  = "x-forwarded-for"
)

// trustedProxyPolicy is the resolved policy. A nil Ranges slice means "use the
// built-in peer check".
type trustedProxyPolicy struct {
	ranges       []netip.Prefix
	cloudflare   bool
	realIP       bool
	forwardedFor bool
}

var currentProxyPolicy atomic.Pointer[trustedProxyPolicy]

func defaultProxyPolicy() *trustedProxyPolicy {
	return &trustedProxyPolicy{cloudflare: true, realIP: true, forwardedFor: true}
}

// TrustedProxyPolicy returns the active policy, parsing the environment once.
func TrustedProxyPolicy() *trustedProxyPolicy {
	if policy := currentProxyPolicy.Load(); policy != nil {
		return policy
	}

	policy := defaultProxyPolicy()
	if raw := strings.TrimSpace(os.Getenv(envTrustedProxyRanges)); raw != "" {
		policy.ranges = parseProxyRanges(raw)
	}
	if raw := strings.TrimSpace(os.Getenv(envTrustProxyHeaders)); raw != "" {
		policy.cloudflare, policy.realIP, policy.forwardedFor = false, false, false
		for _, name := range strings.Split(raw, ",") {
			switch strings.ToLower(strings.TrimSpace(name)) {
			case headerCloudflare:
				policy.cloudflare = true
			case headerRealIP:
				policy.realIP = true
			case headerForwarded:
				policy.forwardedFor = true
			}
		}
	}
	currentProxyPolicy.CompareAndSwap(nil, policy)

	return currentProxyPolicy.Load()
}

// SetTrustedProxyPolicy installs an explicit policy. Tests use it; production reads
// the environment through TrustedProxyPolicy.
func SetTrustedProxyPolicy(ranges []string, cloudflare, realIP, forwardedFor bool) {
	policy := &trustedProxyPolicy{
		ranges:       parseProxyRanges(strings.Join(ranges, ",")),
		cloudflare:   cloudflare,
		realIP:       realIP,
		forwardedFor: forwardedFor,
	}
	currentProxyPolicy.Store(policy)
}

// parseProxyRanges turns a comma-separated CIDR list into prefixes. Entries that do
// not parse are ignored rather than silently trusting a wider set.
func parseProxyRanges(raw string) []netip.Prefix {
	prefixes := make([]netip.Prefix, 0)
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(entry); err == nil {
			prefixes = append(prefixes, prefix.Masked())
		}
	}

	return prefixes
}

// peerIsTrusted reports whether a peer may supply forwarding headers.
func peerIsTrusted(address string) bool {
	policy := TrustedProxyPolicy()
	if len(policy.ranges) == 0 {
		return isTrustedProxyPeer(address)
	}

	addr, err := netip.ParseAddr(strings.TrimSpace(address))
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	for _, prefix := range policy.ranges {
		if prefix.Contains(addr) {
			return true
		}
	}

	return false
}
