package livestatus

import (
	"testing"
	"time"

	"github.com/owncast/owncast/models"
)

func TestLiveViewerFromViewerCopiesHLSPlaybackQuality(t *testing.T) {
	bitrate := 1328.0
	fps := 24.0
	variantIndex := 0
	viewer := models.Viewer{
		FirstSeen:    time.Now(),
		LastSeen:     time.Now(),
		ClientID:     "hls-viewer",
		IPAddress:    "127.0.0.1",
		UserAgent:    "HLSTestViewer/1.0",
		VariantIndex: &variantIndex,
		VariantName:  "720p",
		BitrateKbps:  &bitrate,
		Resolution:   "1280x720",
		FPS:          &fps,
	}

	row := liveViewerFromViewer(viewer)

	if row.Protocol != models.LiveProtocolHLS {
		t.Fatalf("protocol = %q, want %q", row.Protocol, models.LiveProtocolHLS)
	}
	if row.BitrateKbps == nil || *row.BitrateKbps != bitrate {
		t.Fatalf("bitrate = %v, want %.0f", row.BitrateKbps, bitrate)
	}
	if row.Resolution != "1280x720" {
		t.Fatalf("resolution = %q, want 1280x720", row.Resolution)
	}
	if row.FPS == nil || *row.FPS != fps {
		t.Fatalf("fps = %v, want %.0f", row.FPS, fps)
	}
}

func TestLiveViewerFromViewerKeepsUnknownQualityEmpty(t *testing.T) {
	row := liveViewerFromViewer(models.Viewer{
		FirstSeen: time.Now(),
		ClientID:  "master-only",
		IPAddress: "127.0.0.1",
	})

	if row.BitrateKbps != nil || row.Resolution != "" || row.FPS != nil {
		t.Fatalf("master-only viewer invented quality: %+v", row)
	}
}
