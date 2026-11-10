package geoip

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/oschwald/geoip2-golang"
)

func TestLocalAddressLookup(t *testing.T) {
	client := NewClient()
	local := []string{
		"127.0.0.1", "::1", // loopback
		"192.168.50.7", "10.1.2.3", "172.16.0.9", // RFC 1918
		"100.64.1.2",         // CGNAT
		"169.254.10.5",       // link-local
		"fd00::1", "fe80::1", // IPv6 ULA and link-local
	}
	for _, ip := range local {
		details := client.GetGeoFromIP(ip)
		if details == nil {
			t.Fatalf("expected local details for %s", ip)
		}
		if details.CountryCode != "N/A" || details.RegionName != localRegionName {
			t.Errorf("unexpected local details for %s: %+v", ip, details)
		}
	}
}

func TestIsLocalAddress(t *testing.T) {
	local := []string{"127.0.0.1", "::1", "192.168.1.1", "10.0.0.1", "172.31.255.255", "100.64.0.1", "169.254.1.1", "fd12::1", "fe80::abcd"}
	for _, ip := range local {
		if !IsLocalAddress(ip) {
			t.Errorf("%s should be local", ip)
		}
	}
	public := []string{"8.8.8.8", "1.1.1.1", "172.32.0.1", "192.169.0.1", "2001:4860:4860::8888", "", "not-an-ip"}
	for _, ip := range public {
		if IsLocalAddress(ip) {
			t.Errorf("%s should not be local", ip)
		}
	}
}

func TestGeoDetailsFromCityRecord(t *testing.T) {
	record := &geoip2.City{}
	record.Country.IsoCode = "US"
	record.City.Names = map[string]string{"en": "San Francisco"}
	record.Location.TimeZone = "America/Los_Angeles"

	details := geoDetailsFromCityRecord(record)
	if details == nil {
		t.Fatal("expected details for a country-level record")
	}
	if details.CountryCode != "US" {
		t.Errorf("CountryCode = %q, want US", details.CountryCode)
	}
	if details.City != "San Francisco" {
		t.Errorf("City = %q, want San Francisco", details.City)
	}
	if details.RegionName != "Unknown" {
		t.Errorf("RegionName = %q, want the Unknown fallback", details.RegionName)
	}
	if details.TimeZone != "America/Los_Angeles" {
		t.Errorf("TimeZone = %q", details.TimeZone)
	}
}

func TestGeoDetailsFromCityRecordRejections(t *testing.T) {
	noCountry := &geoip2.City{}
	if details := geoDetailsFromCityRecord(noCountry); details != nil {
		t.Errorf("expected nil for a record without a country, got %+v", details)
	}

	anonymous := &geoip2.City{}
	anonymous.Country.IsoCode = "US"
	anonymous.Traits.IsAnonymousProxy = true
	if details := geoDetailsFromCityRecord(anonymous); details != nil {
		t.Errorf("expected nil for an anonymous proxy, got %+v", details)
	}
}

func TestGeoDetailsFromCountryRecord(t *testing.T) {
	record := &geoip2.Country{}
	record.Country.IsoCode = "DE"
	details := geoDetailsFromCountryRecord(record)
	if details == nil || details.CountryCode != "DE" {
		t.Fatalf("unexpected country record mapping: %+v", details)
	}
	if details.City != "" || details.RegionName != "Unknown" || details.TimeZone != "" {
		t.Errorf("country-only mapping must not invent city data: %+v", details)
	}
}

func TestDatabaseMetadataMissingDatabase(t *testing.T) {
	missingPath := filepath.Join(t.TempDir(), "missing.mmdb")
	client := NewClientWithDatabasePath(missingPath)

	metadata := client.DatabaseMetadata()
	if metadata.Configured {
		t.Fatalf("expected missing database to be unconfigured: %+v", metadata)
	}
	if metadata.Path != missingPath {
		t.Errorf("Path = %q, want %q", metadata.Path, missingPath)
	}
	if metadata.Error == "" {
		t.Error("expected a safe error reason for the missing database")
	}
	if len(metadata.AvailableFields()) != 0 {
		t.Errorf("unconfigured database lists fields: %v", metadata.AvailableFields())
	}
	if metadata.HasCity || metadata.HasCountry {
		t.Errorf("unconfigured database claims capabilities: %+v", metadata)
	}

	// Lookups are disabled and never retried per request once the open fails.
	if details := client.GetGeoFromIP("198.51.100.7"); details != nil {
		t.Errorf("expected nil lookup with no database, got %+v", details)
	}
	if client.HasCityDatabase() {
		t.Error("HasCityDatabase() = true with no database")
	}
	if len(client.AvailableFields()) != 0 {
		t.Errorf("AvailableFields() = %v with no database", client.AvailableFields())
	}
	if err := client.Close(); err != nil {
		t.Errorf("Close() on an unopened client: %v", err)
	}
}

func TestAvailableFieldsByCapability(t *testing.T) {
	city := DatabaseMetadata{Configured: true, HasCity: true, HasCountry: true}
	fields := city.AvailableFields()
	joined := strings.Join(fields, ",")
	for _, want := range []string{"country", "region", "city", "timezone"} {
		if !strings.Contains(joined, want) {
			t.Errorf("city database fields %v missing %q", fields, want)
		}
	}

	countryOnly := DatabaseMetadata{Configured: true, HasCountry: true}
	fields = countryOnly.AvailableFields()
	if len(fields) != 1 || fields[0] != "country" {
		t.Errorf("country-only fields = %v, want [country]", fields)
	}
}

func TestClientStringNeverIncludesViewerIP(t *testing.T) {
	client := NewClientWithDatabasePath(filepath.Join(t.TempDir(), "missing.mmdb"))
	value := client.String()
	if !strings.Contains(value, "geoip database") {
		t.Errorf("String() = %q", value)
	}
	if strings.Contains(value, "127.0.0.1") || strings.Contains(value, "198.51.100.7") {
		t.Errorf("String() leaked a viewer IP: %q", value)
	}
}
