package webrtc

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/owncast/owncast/models"
	"github.com/pion/webrtc/v4"
)

const domainResolveTimeout = 3 * time.Second

// resolveDomainIPs resolves A/AAAA records for the configured domain. Go's
// resolver does not expose DNS TTLs, so re-resolution is interval based: the
// engine resolves at start and the admin "test domain" action can force a
// refresh by restarting the service.
func resolveDomainIPs(ctx context.Context, domain string) ([]netip.Addr, error) {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return nil, nil
	}

	lookupCtx, cancel := context.WithTimeout(ctx, domainResolveTimeout)
	defer cancel()

	raw, err := net.DefaultResolver.LookupNetIP(lookupCtx, "ip", domain)
	if err != nil {
		return nil, err
	}

	addrs := make([]netip.Addr, 0, len(raw))
	for _, addr := range raw {
		addrs = append(addrs, addr.Unmap())
	}

	return addrs, nil
}

// advertisedIPs returns the IPs that should be advertised as ICE host
// candidates: the explicit publicIPs override wins, otherwise the domain is
// resolved. An empty result means "advertise the locally gathered addresses".
func advertisedIPs(ctx context.Context, cfg models.WebRTCConfig) ([]netip.Addr, error) {
	if ips := ParsePublicIPs(cfg); len(ips) > 0 {
		return ips, nil
	}

	return resolveDomainIPs(ctx, cfg.Domain)
}

// addressRewriteRules builds Pion ICE address rewrite rules that append the
// public/domain IPs as additional host candidates. The locally gathered LAN
// candidates are kept, so a viewer on the same network races the LAN UDP path
// against the public one instead of requiring NAT hairpin. Public clients still
// receive the public-IP candidate required by the domain binding.
func addressRewriteRules(ips []netip.Addr) []webrtc.ICEAddressRewriteRule {
	v4, v6 := addrsByFamily(ips)
	rules := make([]webrtc.ICEAddressRewriteRule, 0, 2)

	if len(v4) > 0 {
		rules = append(rules, webrtc.ICEAddressRewriteRule{
			External:        v4,
			AsCandidateType: webrtc.ICECandidateTypeHost,
			Mode:            webrtc.ICEAddressRewriteAppend,
			Networks:        []webrtc.NetworkType{webrtc.NetworkTypeUDP4, webrtc.NetworkTypeTCP4},
		})
	}
	if len(v6) > 0 {
		rules = append(rules, webrtc.ICEAddressRewriteRule{
			External:        v6,
			AsCandidateType: webrtc.ICECandidateTypeHost,
			Mode:            webrtc.ICEAddressRewriteAppend,
			Networks:        []webrtc.NetworkType{webrtc.NetworkTypeUDP6, webrtc.NetworkTypeTCP6},
		})
	}

	return rules
}

// hostMatchesDomain reports whether the request Host belongs to the configured
// domain or one of the allowed origins. It is only enforced when strictDomain
// is on.
func hostMatchesDomain(r *http.Request, cfg models.WebRTCConfig) bool {
	host := normalizeHost(r.Host)
	if host == "" {
		return false
	}
	if domain := normalizeHost(cfg.Domain); domain != "" && host == domain {
		return true
	}
	for _, origin := range cfg.AllowedOrigins {
		if originHost := hostFromOrigin(origin); originHost != "" && host == originHost {
			return true
		}
	}

	return false
}

// requestIsSameOrigin reports whether the request's Origin header belongs to
// the same host as the request Host. A missing Origin counts as non-browser
// traffic and is handled by originAllowed directly.
func requestIsSameOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" || origin == "null" {
		return false
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}

	return normalizeHost(parsed.Host) == normalizeHost(r.Host)
}

// originAllowed reports whether an Origin header is acceptable.
//
// A missing Origin is allowed (native clients and same-origin navigations may
// omit it). A strict same-origin request (Origin host == request Host) is
// always allowed: it is the normal viewer page case and needs no allowlist.
// Cross-origin requests are allowed only when they match an entry of
// allowedOrigins (or the configured domain, whose origin is implicitly
// trusted). strictDomain enforcement is separate and lives in the handler.
func originAllowed(r *http.Request, cfg models.WebRTCConfig) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	if origin == "null" {
		return false
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	if requestIsSameOrigin(r) {
		return true
	}
	originHost := normalizeHost(parsed.Host)
	if domain := normalizeHost(cfg.Domain); domain != "" && originHost == domain {
		return true
	}
	for _, allowed := range cfg.AllowedOrigins {
		if sameOrigin(origin, allowed) {
			return true
		}
	}

	return false
}

func sameOrigin(a, b string) bool {
	ua, err := url.Parse(strings.TrimSpace(a))
	if err != nil {
		return false
	}
	ub, err := url.Parse(strings.TrimSpace(b))
	if err != nil {
		return false
	}
	if !strings.EqualFold(ua.Scheme, ub.Scheme) {
		return false
	}

	return normalizeHost(ua.Host) == normalizeHost(ub.Host)
}

func normalizeHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "" {
		return ""
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}

	return strings.TrimSuffix(host, ".")
}

func hostFromOrigin(origin string) string {
	parsed, err := url.Parse(strings.TrimSpace(origin))
	if err != nil {
		return ""
	}

	return normalizeHost(parsed.Host)
}

// allowedOriginHeader returns the value for Access-Control-Allow-Origin. Only
// cross-origin requests that are allowed get CORS headers; a same-origin
// request needs none (and echoing its Origin would be misleading).
func allowedOriginHeader(r *http.Request, cfg models.WebRTCConfig) string {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" || requestIsSameOrigin(r) {
		return ""
	}
	if originAllowed(r, cfg) {
		return origin
	}

	return ""
}
