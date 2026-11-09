package livestatus

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/geoip"
)

type fakeGeoIPProvider struct {
	status models.GeoIPStatus
}

func (f fakeGeoIPProvider) GeoIPStatus() models.GeoIPStatus {
	return f.status
}

func TestGeoIPStatusFromProvider(t *testing.T) {
	if status := geoIPStatusFromProvider(nil); !reflect.DeepEqual(status, models.GeoIPStatus{}) {
		t.Errorf("nil provider status = %+v, want zero", status)
	}

	fake := fakeGeoIPProvider{status: models.GeoIPStatus{
		Configured:   true,
		DatabaseType: "GeoLite2-City",
		BuildDate:    "2026-10-01",
	}}
	status := geoIPStatusFromProvider(fake)
	if !status.Configured || status.DatabaseType != "GeoLite2-City" || status.BuildDate != "2026-10-01" {
		t.Errorf("fake provider status = %+v", status)
	}
}

func TestGeoIPWarnings(t *testing.T) {
	if warnings := geoIPWarnings(models.GeoIPStatus{Configured: true}); len(warnings) != 0 {
		t.Errorf("configured database produced warnings: %+v", warnings)
	}

	warnings := geoIPWarnings(models.GeoIPStatus{})
	if len(warnings) != 1 {
		t.Fatalf("expected one missing-database warning, got %+v", warnings)
	}
	if warnings[0].Code != "geoip_database_missing" {
		t.Errorf("warning code = %q", warnings[0].Code)
	}
	if !strings.Contains(warnings[0].Message, "N/A") || !strings.Contains(warnings[0].Message, "GeoLite2-City") {
		t.Errorf("warning message = %q", warnings[0].Message)
	}
}

func TestLiveViewerFromViewerMapsCity(t *testing.T) {
	firstSeen := time.Now().Add(-90 * time.Second)
	viewer := models.Viewer{
		ClientID:  "client-1",
		IPAddress: "203.0.113.5",
		UserAgent: "test-agent",
		FirstSeen: firstSeen,
		Geo: &geoip.GeoDetails{
			CountryCode: "US",
			RegionName:  "California",
			TimeZone:    "America/Los_Angeles",
			City:        "San Francisco",
		},
	}

	row := liveViewerFromViewer(viewer)
	if row.ClientID != "client-1" || row.IPAddress != "203.0.113.5" {
		t.Errorf("identity fields not mapped: %+v", row)
	}
	if row.Country != "US" || row.Region != "California" || row.City != "San Francisco" {
		t.Errorf("location fields not mapped: %+v", row)
	}
	if !row.LocationApprox {
		t.Error("expected LocationApprox for a GeoIP-derived location")
	}
	if row.Protocol != models.LiveProtocolHLS {
		t.Errorf("protocol = %q", row.Protocol)
	}
	if row.Source != string(models.PlaybackSourceServer) {
		t.Errorf("source = %q", row.Source)
	}
	if row.DurationSeconds < 89 || row.DurationSeconds > 91 {
		t.Errorf("duration = %v, want about 90s", row.DurationSeconds)
	}
}

func TestLiveViewerFromViewerWithoutGeoIP(t *testing.T) {
	viewer := models.Viewer{ClientID: "client-2", IPAddress: "198.51.100.9", FirstSeen: time.Now()}
	row := liveViewerFromViewer(viewer)
	if row.Country != "" || row.Region != "" || row.City != "" {
		t.Errorf("location fields must be empty without GeoIP: %+v", row)
	}
	if row.LocationApprox {
		t.Error("LocationApprox must be false without GeoIP")
	}
}

func TestSourceStatusMapsSRTMediaDetails(t *testing.T) {
	hasBFrames := false
	broadcaster := &models.Broadcaster{StreamDetails: models.InboundStreamDetails{
		Width:           1280,
		Height:          720,
		VideoFramerate:  30,
		VideoCodec:      "H.264",
		VideoProfile:    "main",
		VideoLevel:      "3.1",
		AudioCodec:      "AAC",
		VideoBitrate:    2520,
		AudioBitrate:    130,
		HasBFrames:      &hasBFrames,
		AudioProfile:    "LC",
		AudioSampleRate: 48000,
		AudioChannels:   2,
	}}

	status := sourceStatus(broadcaster)
	if status.Width == nil || *status.Width != 1280 || status.Height == nil || *status.Height != 720 {
		t.Errorf("resolution not mapped: %+v", status)
	}
	if status.VideoBitrateKbps == nil || *status.VideoBitrateKbps != 2520 {
		t.Errorf("video bitrate = %v", status.VideoBitrateKbps)
	}
	if status.AudioBitrateKbps == nil || *status.AudioBitrateKbps != 130 {
		t.Errorf("audio bitrate = %v", status.AudioBitrateKbps)
	}
	if status.AudioProfile != "LC" {
		t.Errorf("audio profile = %q", status.AudioProfile)
	}
	if status.AudioSampleRate == nil || *status.AudioSampleRate != 48000 {
		t.Errorf("audio sample rate = %v", status.AudioSampleRate)
	}
	if status.AudioChannels == nil || *status.AudioChannels != 2 {
		t.Errorf("audio channels = %v", status.AudioChannels)
	}
	if status.HasBFrames == nil || *status.HasBFrames {
		t.Errorf("B-frame verdict not mapped: %v", status.HasBFrames)
	}
}

func TestSourceStatusRTMPBackwardCompatible(t *testing.T) {
	// The legacy RTMP metadata shape carries bitrates but no AAC details; the
	// new detail fields must stay unset so the JSON stays additive-only.
	broadcaster := &models.Broadcaster{StreamDetails: models.InboundStreamDetails{
		VideoCodec:   "H.264",
		AudioCodec:   "AAC",
		VideoBitrate: 8000,
		AudioBitrate: 320,
	}}

	status := sourceStatus(broadcaster)
	if status.VideoBitrateKbps == nil || *status.VideoBitrateKbps != 8000 {
		t.Errorf("video bitrate = %v", status.VideoBitrateKbps)
	}
	if status.AudioBitrateKbps == nil || *status.AudioBitrateKbps != 320 {
		t.Errorf("audio bitrate = %v", status.AudioBitrateKbps)
	}
	if status.AudioProfile != "" || status.AudioSampleRate != nil || status.AudioChannels != nil {
		t.Errorf("RTMP payload must not gain AAC detail keys: %+v", status)
	}
}
