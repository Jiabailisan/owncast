package srt

import (
	"fmt"
	"net/netip"
)

// cidrSet is an immutable allow/deny filter over peer addresses. Deny entries
// win over allow entries; an empty allow list means "allow every address not
// denied". Instances are built once per configuration change and swapped
// atomically by the service.
type cidrSet struct {
	allow []netip.Prefix
	deny  []netip.Prefix
}

// cidrDecision describes why an address was rejected, for log reasons only.
type cidrDecision struct {
	allowed bool
	reason  string
}

// newCIDRSet parses and normalizes the configured CIDR lists. Both lists may
// be empty.
func newCIDRSet(allowed, denied []string) (*cidrSet, error) {
	allow, err := parseCIDRList(allowed)
	if err != nil {
		return nil, fmt.Errorf("allowed CIDRs: %w", err)
	}
	deny, err := parseCIDRList(denied)
	if err != nil {
		return nil, fmt.Errorf("denied CIDRs: %w", err)
	}
	return &cidrSet{allow: allow, deny: deny}, nil
}

func parseCIDRList(entries []string) ([]netip.Prefix, error) {
	prefixes := make([]netip.Prefix, 0, len(entries))
	for _, entry := range entries {
		prefix, err := netip.ParsePrefix(entry)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q: %w", entry, err)
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return prefixes, nil
}

// Check reports whether addr is allowed to attempt authentication.
func (c *cidrSet) Check(addr netip.Addr) cidrDecision {
	if c == nil {
		return cidrDecision{allowed: true}
	}

	addr = addr.Unmap()

	for _, prefix := range c.deny {
		if prefix.Contains(addr) {
			return cidrDecision{allowed: false, reason: reasonCIDRDenied}
		}
	}

	if len(c.allow) == 0 {
		return cidrDecision{allowed: true}
	}

	for _, prefix := range c.allow {
		if prefix.Contains(addr) {
			return cidrDecision{allowed: true}
		}
	}

	return cidrDecision{allowed: false, reason: reasonCIDRNotAllowed}
}

// ValidateCIDRs validates both CIDR lists without building a set. It is used
// by the configuration validator so a bad entry is a field-level save error
// instead of a runtime surprise.
func ValidateCIDRs(allowed, denied []string) error {
	if _, err := newCIDRSet(allowed, denied); err != nil {
		return err
	}
	return nil
}
