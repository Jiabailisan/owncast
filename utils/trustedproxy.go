package utils

import (
	"net/netip"
	"os"
	"strings"
	"sync/atomic"

	log "github.com/sirupsen/logrus"
)

// Which forwarding headers may be trusted, and from which peers.
//
// Three states are distinct:
//
//   - OWNCAST_TRUSTED_PROXY_RANGES unset: the built-in peer check applies (loopback
//     plus this host's own addresses), which is what the shipped deployments use.
//   - Set to a valid CIDR list: only those peers may supply headers.
//   - Set to something that does not parse: the configuration is reported and header
//     trust is disabled outright. It never falls back to the built-in set, because a
//     typo must not silently widen who can claim an address.
//
// Header selection is explicit. OWNCAST_TRUST_PROXY_HEADERS lists the headers a
// trusted peer may supply; when it is unset or empty no header is trusted and the
// peer address is used.
//
//	OWNCAST_TRUST_PROXY_HEADERS=cf-connecting-ip,x-forwarded-for
//
// CF-Connecting-IP and X-Real-IP are only safe when the proxy in front overwrites or
// removes the client's own value for that header. Cloudflare does that for
// CF-Connecting-IP; a reverse proxy must be configured to set X-Real-IP rather than
// forward it. X-Forwarded-For is read from the right and only hops inside the trusted
// ranges are skipped, so a client-supplied value at the head of the list is never
// returned; if every hop is trusted, the peer address is used instead.
const (
	envTrustedProxyRanges = "OWNCAST_TRUSTED_PROXY_RANGES"
	envTrustProxyHeaders  = "OWNCAST_TRUST_PROXY_HEADERS"

	headerCloudflare = "cf-connecting-ip"
	headerRealIP     = "x-real-ip"
	headerForwarded  = "x-forwarded-for"
)

// trustedProxyPolicy is the resolved policy. A nil Ranges slice means "use the
// built-in peer check"; Invalid means the configured ranges did not parse and no
// header may be trusted.
type trustedProxyPolicy struct {
	ranges       []netip.Prefix
	invalid      bool
	cloudflare   bool
	realIP       bool
	forwardedFor bool
}

var currentProxyPolicy atomic.Pointer[trustedProxyPolicy]

// TrustedProxyPolicy returns the active policy, resolving the environment once.
func TrustedProxyPolicy() *trustedProxyPolicy {
	if policy := currentProxyPolicy.Load(); policy != nil {
		return policy
	}

	policy := resolveProxyPolicy()
	if !currentProxyPolicy.CompareAndSwap(nil, policy) {
		return currentProxyPolicy.Load()
	}

	return policy
}

func resolveProxyPolicy() *trustedProxyPolicy {
	policy := &trustedProxyPolicy{}

	if raw := strings.TrimSpace(os.Getenv(envTrustedProxyRanges)); raw != "" {
		ranges, err := parseProxyRanges(raw)
		if err != nil {
			log.Errorf("%s is invalid (%v); forwarding headers are not trusted", envTrustedProxyRanges, err)
			policy.invalid = true
		} else {
			policy.ranges = ranges
		}
	}

	for _, name := range strings.Split(os.Getenv(envTrustProxyHeaders), ",") {
		switch strings.ToLower(strings.TrimSpace(name)) {
		case headerCloudflare:
			policy.cloudflare = true
		case headerRealIP:
			policy.realIP = true
		case headerForwarded:
			policy.forwardedFor = true
		}
	}

	return policy
}

// SetTrustedProxyPolicy installs an explicit policy. Tests use it; production reads
// the environment. An empty range list keeps the built-in peer check, and an entry
// that does not parse disables header trust rather than widening it.
func SetTrustedProxyPolicy(ranges []string, cloudflare, realIP, forwardedFor bool) {
	policy := &trustedProxyPolicy{cloudflare: cloudflare, realIP: realIP, forwardedFor: forwardedFor}
	if len(ranges) > 0 {
		parsed, err := parseProxyRanges(strings.Join(ranges, ","))
		if err != nil {
			policy.invalid = true
		} else {
			policy.ranges = parsed
		}
	}
	currentProxyPolicy.Store(policy)
}

// ResetTrustedProxyPolicy clears the resolved policy so the next read re-parses the
// environment. Tests use it between cases.
func ResetTrustedProxyPolicy() {
	currentProxyPolicy.Store(nil)
}

// parseProxyRanges turns a comma-separated CIDR list into prefixes. Any entry that
// does not parse fails the whole list: a partial list would trust less than intended
// in one place and more in another.
func parseProxyRanges(raw string) ([]netip.Prefix, error) {
	prefixes := make([]netip.Prefix, 0)
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(entry)
		if err != nil {
			return nil, err
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	if len(prefixes) == 0 {
		return nil, os.ErrInvalid
	}

	return prefixes, nil
}

// peerIsTrusted reports whether a peer may supply forwarding headers.
func peerIsTrusted(address string) bool {
	policy := TrustedProxyPolicy()
	if policy.invalid {
		return false
	}
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
