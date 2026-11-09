package geoip

import (
	"path/filepath"
	"testing"
)

func TestMergeGeoDetailsFillsMissingFields(t *testing.T) {
	primary := &GeoDetails{CountryCode: "CN", RegionName: "Unknown"}
	secondary := &GeoDetails{
		CountryCode: "CN",
		RegionName:  "Beijing",
		City:        "Beijing",
		TimeZone:    "Asia/Shanghai",
	}

	merged := mergeGeoDetails(primary, secondary)
	if merged == nil {
		t.Fatal("expected merged details")
	}
	if merged.CountryCode != "CN" {
		t.Errorf("CountryCode = %q", merged.CountryCode)
	}
	if merged.RegionName != "Beijing" {
		t.Errorf("RegionName = %q, want Beijing", merged.RegionName)
	}
	if merged.City != "Beijing" {
		t.Errorf("City = %q, want Beijing", merged.City)
	}
	if merged.TimeZone != "Asia/Shanghai" {
		t.Errorf("TimeZone = %q", merged.TimeZone)
	}
	if primary.City != "" || primary.RegionName != "Unknown" {
		t.Errorf("merge mutated the primary value: %+v", primary)
	}
}

func TestMergeGeoDetailsPrimaryWins(t *testing.T) {
	primary := &GeoDetails{CountryCode: "US", RegionName: "California", City: "San Francisco", TimeZone: "America/Los_Angeles"}
	secondary := &GeoDetails{CountryCode: "US", RegionName: "Nevada", City: "Las Vegas", TimeZone: "America/New_York"}

	merged := mergeGeoDetails(primary, secondary)
	if merged.City != "San Francisco" || merged.RegionName != "California" || merged.TimeZone != "America/Los_Angeles" {
		t.Errorf("primary values must win: %+v", merged)
	}
}

func TestMergeGeoDetailsNilHandling(t *testing.T) {
	if merged := mergeGeoDetails(nil, &GeoDetails{City: "X"}); merged != nil {
		t.Errorf("nil primary must stay nil, got %+v", merged)
	}
	primary := &GeoDetails{CountryCode: "DE"}
	if merged := mergeGeoDetails(primary, nil); merged != primary {
		t.Errorf("nil secondary must return primary unchanged, got %+v", merged)
	}
}

func TestMergeGeoDetailsFillsEmptyCountryOnly(t *testing.T) {
	primary := &GeoDetails{}
	secondary := &GeoDetails{CountryCode: "SE", RegionName: "Ostergotland", City: "Linkoping"}
	merged := mergeGeoDetails(primary, secondary)
	if merged.CountryCode != "SE" || merged.City != "Linkoping" || merged.RegionName != "Ostergotland" {
		t.Errorf("empty primary should inherit enrichment: %+v", merged)
	}
}

func TestDefaultSecondaryDatabasePaths(t *testing.T) {
	found := map[string]bool{}
	for _, path := range DefaultSecondaryDatabasePaths {
		found[path] = true
	}
	if !found["data/dbip-city-lite.mmdb"] {
		t.Error("default secondary paths must include the lower-case DB-IP name")
	}
	if !found["data/DBIP-City-Lite.mmdb"] {
		t.Error("default secondary paths must include the capitalized DB-IP name")
	}
}

func TestSecondaryPathFiltering(t *testing.T) {
	primary := filepath.Join(t.TempDir(), "primary.mmdb")
	client := NewClientWithDatabasePaths(primary, []string{primary, "", "/tmp/secondary.mmdb"})
	if len(client.secondaryPaths) != 1 || client.secondaryPaths[0] != "/tmp/secondary.mmdb" {
		t.Errorf("secondary candidates = %v, want only /tmp/secondary.mmdb", client.secondaryPaths)
	}
	if client.primaryPath != primary {
		t.Errorf("primary path = %q", client.primaryPath)
	}
}

func TestSingleExplicitDatabaseHasNoSecondary(t *testing.T) {
	client := NewClientWithDatabasePath(filepath.Join(t.TempDir(), "missing.mmdb"))
	if got := len(client.secondaryPaths); got != 0 {
		t.Errorf("NewClientWithDatabasePath must not auto-detect secondaries, got %d", got)
	}
}
