package srt

import (
	"net/netip"
	"testing"
)

func mustAddr(t *testing.T, value string) netip.Addr {
	t.Helper()
	addr, err := netip.ParseAddr(value)
	if err != nil {
		t.Fatalf("ParseAddr(%q): %v", value, err)
	}
	return addr
}

func TestCIDRSetEmptyAllowsEverything(t *testing.T) {
	set, err := newCIDRSet(nil, nil)
	if err != nil {
		t.Fatalf("newCIDRSet: %v", err)
	}
	for _, address := range []string{"127.0.0.1", "10.1.2.3", "192.168.1.1", "2001:db8::1"} {
		if decision := set.Check(mustAddr(t, address)); !decision.allowed {
			t.Errorf("expected %s to be allowed, got %+v", address, decision)
		}
	}
}

func TestCIDRSetDenyWinsOverAllow(t *testing.T) {
	set, err := newCIDRSet([]string{"10.0.0.0/8"}, []string{"10.1.0.0/16"})
	if err != nil {
		t.Fatalf("newCIDRSet: %v", err)
	}

	if decision := set.Check(mustAddr(t, "10.2.3.4")); !decision.allowed {
		t.Errorf("10.2.3.4 should be allowed: %+v", decision)
	}
	if decision := set.Check(mustAddr(t, "10.1.2.3")); decision.allowed || decision.reason != reasonCIDRDenied {
		t.Errorf("10.1.2.3 should be denied by the deny list: %+v", decision)
	}
	if decision := set.Check(mustAddr(t, "192.168.1.1")); decision.allowed || decision.reason != reasonCIDRNotAllowed {
		t.Errorf("192.168.1.1 should be denied by the allow list: %+v", decision)
	}
}

func TestCIDRSetUnmapsIPv4InIPv6(t *testing.T) {
	set, err := newCIDRSet([]string{"127.0.0.0/8"}, nil)
	if err != nil {
		t.Fatalf("newCIDRSet: %v", err)
	}
	mapped := netip.MustParseAddr("::ffff:127.0.0.1")
	if decision := set.Check(mapped); !decision.allowed {
		t.Errorf("IPv4-mapped address should match the IPv4 CIDR: %+v", decision)
	}
}

func TestCIDRSetIPv6(t *testing.T) {
	set, err := newCIDRSet([]string{"2001:db8::/32"}, nil)
	if err != nil {
		t.Fatalf("newCIDRSet: %v", err)
	}
	if decision := set.Check(mustAddr(t, "2001:db8::42")); !decision.allowed {
		t.Errorf("IPv6 address should be allowed: %+v", decision)
	}
	if decision := set.Check(mustAddr(t, "2001:db9::42")); decision.allowed {
		t.Errorf("IPv6 address outside the allow list should be denied: %+v", decision)
	}
}

func TestNewCIDRSetRejectsInvalidEntries(t *testing.T) {
	if _, err := newCIDRSet([]string{"not-a-cidr"}, nil); err == nil {
		t.Error("expected an error for an invalid allow entry")
	}
	if _, err := newCIDRSet(nil, []string{"10.0.0.0/33"}); err == nil {
		t.Error("expected an error for an invalid deny entry")
	}
	if err := ValidateCIDRs([]string{"10.0.0.0/8"}, nil); err != nil {
		t.Errorf("ValidateCIDRs returned an unexpected error: %v", err)
	}
}
