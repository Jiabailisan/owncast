package settings

import "testing"

func TestFieldLabelPluralAcronyms(t *testing.T) {
	cases := map[string]string{
		"publicIPs":    "Public IPs",
		"allowedCIDRs": "Allowed CIDRs",
		"tcpEnabled":   "ICE-TCP enabled",
	}
	for key, want := range cases {
		if got := fieldLabel(key); got != want {
			t.Errorf("fieldLabel(%q) = %q, want %q", key, got, want)
		}
	}
}
