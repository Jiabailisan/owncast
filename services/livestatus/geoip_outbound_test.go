package livestatus

import (
	"testing"
	"time"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/geoip"
)

func TestGeoIPStatusFromDatabaseMetadata(t *testing.T) {
	metadata := geoip.DatabaseMetadata{
		Configured:   true,
		Path:         "data/GeoLite2-City.mmdb",
		DatabaseType: "GeoLite2-Country",
		BuildDate:    "2026-01-01",
		HasCity:      true,
		Sources: []geoip.DatabaseSource{
			{Role: geoip.DatabaseRolePrimary, Configured: true, Path: "data/GeoLite2-City.mmdb", DatabaseType: "GeoLite2-Country", BuildDate: "2026-01-01", HasCity: false, HasCountry: true},
			{Role: geoip.DatabaseRoleSecondary, Configured: true, Path: "data/dbip-city-lite.mmdb", DatabaseType: "GeoIP2-City", BuildDate: "2026-02-01", HasCity: true, HasCountry: true},
		},
	}

	status := GeoIPStatusFromDatabaseMetadata(metadata)
	if !status.Configured || status.DatabaseType != "GeoLite2-Country" || status.BuildDate != "2026-01-01" {
		t.Errorf("top-level status = %+v", status)
	}
	if len(status.Sources) != 2 {
		t.Fatalf("sources = %+v, want 2", status.Sources)
	}
	if status.Sources[0].Role != geoip.DatabaseRolePrimary || status.Sources[1].Path != "data/dbip-city-lite.mmdb" {
		t.Errorf("sources not mapped in order: %+v", status.Sources)
	}
	if !status.Sources[1].HasCity {
		t.Error("secondary source should report city capability")
	}

	unconfigured := GeoIPStatusFromDatabaseMetadata(geoip.DatabaseMetadata{
		Configured: false,
		Sources:    []geoip.DatabaseSource{{Role: geoip.DatabaseRolePrimary, Path: "data/missing.mmdb", Error: "database file not found"}},
	})
	if unconfigured.Configured || len(unconfigured.Sources) != 1 {
		t.Errorf("unconfigured status = %+v", unconfigured)
	}
}

func boolValue(value bool) *bool { return &value }

func TestOutboundStatusTranscodedAndPassthrough(t *testing.T) {
	broadcast := &models.CurrentBroadcast{
		OutputSettings: []models.StreamOutputVariant{
			{VideoBitrate: 1200, AudioBitrate: 128, Framerate: 24, CPUUsageLevel: 2},
			{IsVideoPassthrough: true, IsAudioPassthrough: true},
		},
	}
	broadcaster := &models.Broadcaster{
		StreamDetails: models.InboundStreamDetails{VideoBitrate: 5000, AudioBitrate: 160},
	}

	status := outboundStatus(broadcast, broadcaster, "libx264", true)
	if len(status.Streams) != 2 {
		t.Fatalf("streams = %+v, want 2", status.Streams)
	}

	transcoded := status.Streams[0]
	if transcoded.VideoEncoder != "libx264" || transcoded.AudioEncoder != "aac" {
		t.Errorf("transcoded encoders = %q/%q", transcoded.VideoEncoder, transcoded.AudioEncoder)
	}
	if transcoded.VideoBitrateKbps == nil || *transcoded.VideoBitrateKbps != 1200 {
		t.Errorf("transcoded video bitrate = %v", transcoded.VideoBitrateKbps)
	}
	if transcoded.AudioBitrateKbps == nil || *transcoded.AudioBitrateKbps != 128 {
		t.Errorf("transcoded audio bitrate = %v", transcoded.AudioBitrateKbps)
	}
	if len(transcoded.Protocols) != 2 || transcoded.Protocols[0] != "hls" || transcoded.Protocols[1] != "webrtc" {
		t.Errorf("transcoded protocols = %v", transcoded.Protocols)
	}

	passthrough := status.Streams[1]
	if passthrough.VideoEncoder != "copy" || passthrough.AudioEncoder != "copy" {
		t.Errorf("passthrough encoders = %q/%q", passthrough.VideoEncoder, passthrough.AudioEncoder)
	}
	if passthrough.VideoBitrateKbps == nil || *passthrough.VideoBitrateKbps != 5000 {
		t.Errorf("passthrough video bitrate = %v, want source 5000", passthrough.VideoBitrateKbps)
	}
	if passthrough.AudioBitrateKbps == nil || *passthrough.AudioBitrateKbps != 160 {
		t.Errorf("passthrough audio bitrate = %v, want source 160", passthrough.AudioBitrateKbps)
	}
}

func TestOutboundStatusUnknownValuesAreNil(t *testing.T) {
	broadcast := &models.CurrentBroadcast{
		OutputSettings: []models.StreamOutputVariant{
			{IsVideoPassthrough: true, IsAudioPassthrough: true},
		},
	}

	status := outboundStatus(broadcast, nil, "libx264", true)
	stream := status.Streams[0]
	if stream.VideoBitrateKbps != nil || stream.AudioBitrateKbps != nil {
		t.Errorf("unknown passthrough bitrates must be nil (N/A), got %+v", stream)
	}
	if stream.VideoEncoder != "copy" || stream.AudioEncoder != "copy" {
		t.Errorf("encoders = %q/%q", stream.VideoEncoder, stream.AudioEncoder)
	}
}

func TestOutboundStatusExplicitWebRTCMapping(t *testing.T) {
	broadcast := &models.CurrentBroadcast{
		OutputSettings: []models.StreamOutputVariant{
			{
				VideoBitrate: 900,
				Outputs:      &models.VariantOutputs{WebRTC: &models.ProtocolOutputSettings{Enabled: boolValue(true)}},
			},
			{
				VideoBitrate: 500,
				Outputs:      &models.VariantOutputs{WebRTC: &models.ProtocolOutputSettings{Enabled: boolValue(false)}},
			},
		},
	}

	status := outboundStatus(broadcast, nil, "h264_nvenc", true)
	if len(status.Streams) != 2 {
		t.Fatalf("streams = %+v", status.Streams)
	}
	// Explicit mapping exists: the default variant must not implicitly inherit
	// WebRTC; only the explicitly enabled one gets it.
	if len(status.Streams[0].Protocols) != 2 || status.Streams[0].Protocols[1] != "webrtc" {
		t.Errorf("explicitly enabled protocols = %v", status.Streams[0].Protocols)
	}
	if len(status.Streams[1].Protocols) != 1 || status.Streams[1].Protocols[0] != "hls" {
		t.Errorf("explicitly disabled protocols = %v", status.Streams[1].Protocols)
	}
	if status.Streams[0].VideoEncoder != "h264_nvenc" {
		t.Errorf("encoder = %q, want h264_nvenc", status.Streams[0].VideoEncoder)
	}
}

func TestOutboundStatusNoActiveBroadcast(t *testing.T) {
	status := outboundStatus(nil, nil, "", true)
	if status.Streams == nil {
		t.Error("Streams must be a non-nil empty slice")
	}
	if len(status.Streams) != 0 {
		t.Errorf("streams = %+v, want empty", status.Streams)
	}
}

func TestOutboundStatusAudioPassthroughUsesSource(t *testing.T) {
	broadcast := &models.CurrentBroadcast{
		OutputSettings: []models.StreamOutputVariant{{VideoBitrate: 1500, AudioBitrate: 0}},
	}
	broadcaster := &models.Broadcaster{StreamDetails: models.InboundStreamDetails{AudioBitrate: 128}}
	status := outboundStatus(broadcast, broadcaster, "", true)
	stream := status.Streams[0]
	if stream.AudioEncoder != "copy" {
		t.Errorf("audio passthrough encoder = %q", stream.AudioEncoder)
	}
	if stream.AudioBitrateKbps == nil || *stream.AudioBitrateKbps != 128 {
		t.Errorf("audio passthrough bitrate = %v, want source 128", stream.AudioBitrateKbps)
	}
	if stream.VideoEncoder != "libx264" {
		t.Errorf("default encoder = %q, want libx264", stream.VideoEncoder)
	}
	_ = time.Now
}

func TestOutboundStatusWebRTCGloballyDisabled(t *testing.T) {
	broadcast := &models.CurrentBroadcast{
		OutputSettings: []models.StreamOutputVariant{{VideoBitrate: 1000}},
	}
	status := outboundStatus(broadcast, nil, "libx264", false)
	if len(status.Streams) != 1 {
		t.Fatalf("streams = %+v", status.Streams)
	}
	if len(status.Streams[0].Protocols) != 1 || status.Streams[0].Protocols[0] != "hls" {
		t.Errorf("protocols = %v, want [hls] when WebRTC is globally disabled", status.Streams[0].Protocols)
	}
}
