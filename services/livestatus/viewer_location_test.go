package livestatus

import (
	"testing"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/geoip"
)

type stubGeoProvider struct {
	details map[string]*geoip.GeoDetails
}

func (s stubGeoProvider) GeoIPStatus() models.GeoIPStatus { return models.GeoIPStatus{} }

func (s stubGeoProvider) GeoDetails(ip string) *geoip.GeoDetails { return s.details[ip] }

func TestApplyViewerLocationFillsWebRTCRows(t *testing.T) {
	service := &Service{geoIP: stubGeoProvider{details: map[string]*geoip.GeoDetails{
		"192.168.50.1": {CountryCode: "N/A", RegionName: "Local"},
		"203.0.113.9":  {CountryCode: "JP", RegionName: "Hokkaido", City: "Sapporo"},
	}}}

	local := models.LiveViewer{IPAddress: "192.168.50.1"}
	service.applyViewerLocation(&local)
	if local.Region != "Local" || local.Country != "N/A" {
		t.Fatalf("local viewer not labelled: %+v", local)
	}
	if !local.LocationApprox {
		t.Fatal("expected the location to be marked approximate")
	}

	public := models.LiveViewer{IPAddress: "203.0.113.9"}
	service.applyViewerLocation(&public)
	if public.Region != "Hokkaido" || public.City != "Sapporo" || public.Country != "JP" {
		t.Fatalf("public viewer location not filled: %+v", public)
	}

	unknown := models.LiveViewer{IPAddress: "198.51.100.7"}
	service.applyViewerLocation(&unknown)
	if unknown.Country != "" || unknown.Region != "" {
		t.Fatalf("unknown address must stay empty: %+v", unknown)
	}

	// A row that already carries a location is never overwritten.
	existing := models.LiveViewer{IPAddress: "192.168.50.1", Region: "Tokyo"}
	service.applyViewerLocation(&existing)
	if existing.Region != "Tokyo" {
		t.Fatalf("existing location overwritten: %+v", existing)
	}
}
