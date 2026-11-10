package livestatus

import (
	"testing"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/geoip"
)

type stubWebRTCProvider struct {
	rows []models.LiveViewer
}

func (s stubWebRTCProvider) ViewerCount() int                 { return len(s.rows) }
func (s stubWebRTCProvider) LiveViewers() []models.LiveViewer { return s.rows }
func (s stubWebRTCProvider) Warnings() []string               { return nil }

// TestCollectLiveViewersLabelsWebRTCViewers guards the admin Live viewers table
// for WHEP sessions: the WebRTC delivery service knows the viewer address but
// not the location, so the status service must fill it. Without this every
// WebRTC viewer rendered "N/A" no matter where it connected from.
func TestCollectLiveViewersLabelsWebRTCViewers(t *testing.T) {
	service := &Service{
		webrtc: stubWebRTCProvider{rows: []models.LiveViewer{
			{ClientID: "lan", IPAddress: "192.168.50.1", Protocol: "webrtc"},
			{ClientID: "public", IPAddress: "203.0.113.9", Protocol: "webrtc"},
		}},
		geoIP: stubGeoProvider{details: map[string]*geoip.GeoDetails{
			"192.168.50.1": {CountryCode: "N/A", RegionName: "Local"},
			"203.0.113.9":  {CountryCode: "JP", RegionName: "Hokkaido", City: "Sapporo"},
		}},
	}

	rows := service.collectLiveViewers()
	if len(rows) != 2 {
		t.Fatalf("expected two viewer rows, got %d", len(rows))
	}
	if rows[0].Region != "Local" {
		t.Fatalf("LAN WebRTC viewer not labelled Local: %+v", rows[0])
	}
	if rows[1].City != "Sapporo" || rows[1].Region != "Hokkaido" {
		t.Fatalf("public WebRTC viewer location missing: %+v", rows[1])
	}
}
