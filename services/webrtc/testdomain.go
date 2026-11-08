package webrtc

import (
	"context"
	"net/netip"
	"strings"
)

// TestDomainResult is the admin "test domain" response.
type TestDomainResult struct {
	Domain        string   `json:"domain"`
	ResolvedIPs   []string `json:"resolvedIPs"`
	AdvertisedIPs []string `json:"advertisedIPs"`
	StrictDomain  bool     `json:"strictDomain"`
	Error         string   `json:"error"`
}

// TestDomain resolves a domain and reports the IPs that would be advertised as
// ICE host candidates, without changing any state. An empty domain argument
// uses the configured one.
func (s *Service) TestDomain(ctx context.Context, domain string) TestDomainResult {
	cfg := s.Config()
	domain = strings.TrimSpace(domain)
	if domain == "" {
		domain = cfg.Domain
	}

	result := TestDomainResult{Domain: domain, StrictDomain: cfg.StrictDomain}
	if domain == "" && len(cfg.PublicIPs) == 0 {
		result.Error = "no domain and no publicIPs configured"

		return result
	}

	resolved, err := resolveDomainIPs(ctx, domain)
	if err != nil && len(cfg.PublicIPs) == 0 {
		result.Error = err.Error()

		return result
	}
	result.ResolvedIPs = stringifyAddrs(resolved)

	advertised := resolved
	if overrides := ParsePublicIPs(cfg); len(overrides) > 0 {
		advertised = overrides
	}
	result.AdvertisedIPs = stringifyAddrs(advertised)

	return result
}

func stringifyAddrs(addrs []netip.Addr) []string {
	out := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		out = append(out, addr.Unmap().String())
	}

	return out
}
