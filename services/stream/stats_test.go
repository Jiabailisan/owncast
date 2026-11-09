package stream

import (
	"os"
	"testing"
	"time"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/persistence/configrepository"
	"github.com/owncast/owncast/services/datastore"
	"github.com/owncast/owncast/services/geoip"
	"github.com/owncast/owncast/utils"
)

func newStreamStatsTestRepo(t *testing.T) configrepository.ConfigRepository {
	t.Helper()
	ds, err := datastore.SetupPersistence(":memory:", os.TempDir())
	if err != nil {
		t.Fatalf("failed to set up datastore: %v", err)
	}
	return configrepository.New(ds)
}

// newViewerStatsService builds a Service with an in-memory config repository
// seeded with variants and a live viewer map, for HLS viewer quality tests.
func newViewerStatsService(t *testing.T, variants []models.StreamOutputVariant, broadcaster *models.Broadcaster) *Service {
	t.Helper()

	repo := newStreamStatsTestRepo(t)
	if variants != nil {
		if err := repo.SetStreamOutputVariants(variants); err != nil {
			t.Fatalf("failed to seed output variants: %v", err)
		}
	}

	service := &Service{
		configRepository: repo,
		geoIPClient:      geoip.NewClient(),
		stats: &models.Stats{
			StreamConnected: true,
			Viewers:         make(map[string]*models.Viewer),
		},
	}
	service.broadcaster = broadcaster

	return service
}

func newHLSViewer(id string) *models.Viewer {
	return &models.Viewer{
		FirstSeen: time.Now(),
		LastSeen:  time.Now(),
		ClientID:  id,
		IPAddress: "127.0.0.1",
	}
}

func storedViewer(t *testing.T, service *Service, clientID string) models.Viewer {
	t.Helper()

	viewers := service.GetActiveViewers()
	viewer, ok := viewers[clientID]
	if !ok {
		t.Fatalf("viewer %q is not tracked", clientID)
	}

	return viewer
}

func requireBitrateKbps(t *testing.T, got *float64, want float64) {
	t.Helper()

	if got == nil {
		t.Fatalf("bitrate is nil, want %.0f kbps", want)
	}
	if *got != want {
		t.Fatalf("bitrate = %.0f kbps, want %.0f kbps", *got, want)
	}
}

func requireFPS(t *testing.T, got *float64, want float64) {
	t.Helper()

	if got == nil {
		t.Fatalf("fps is nil, want %.0f", want)
	}
	if *got != want {
		t.Fatalf("fps = %.0f, want %.0f", *got, want)
	}
}

func requireNoQuality(t *testing.T, viewer models.Viewer) {
	t.Helper()

	if viewer.VariantIndex != nil || viewer.BitrateKbps != nil || viewer.Resolution != "" || viewer.FPS != nil {
		t.Fatalf("viewer has invented quality: %+v", viewer)
	}
}

func transcodeTestVariant(videoBitrate, audioBitrate, width, height, framerate int) models.StreamOutputVariant {
	return models.StreamOutputVariant{
		Name:         "720p30",
		VideoBitrate: videoBitrate,
		AudioBitrate: audioBitrate,
		ScaledWidth:  width,
		ScaledHeight: height,
		Framerate:    framerate,
	}
}

func passthroughTestVariant() models.StreamOutputVariant {
	return models.StreamOutputVariant{
		Name:               "copy",
		IsVideoPassthrough: true,
		IsAudioPassthrough: true,
	}
}

func testSourceDetails() *models.InboundStreamDetails {
	return &models.InboundStreamDetails{
		VideoCodec:     "H.264",
		Width:          1920,
		Height:         1080,
		VideoBitrate:   4500,
		AudioBitrate:   160,
		VideoFramerate: 30,
	}
}

func TestHLSVariantPlaybackQualityTranscode(t *testing.T) {
	variant := transcodeTestVariant(1000, 128, 1280, 720, 30)

	bitrate, resolution, fps := hlsVariantPlaybackQuality(variant, nil)
	requireBitrateKbps(t, bitrate, 1128)
	if resolution != "1280x720" {
		t.Fatalf("resolution = %q, want 1280x720", resolution)
	}
	requireFPS(t, fps, 30)

	defaultFPSVariant := transcodeTestVariant(1000, 128, 1280, 720, 0)
	_, _, defaultFPS := hlsVariantPlaybackQuality(defaultFPSVariant, nil)
	requireFPS(t, defaultFPS, 24)
}

func TestHLSVariantPlaybackQualityUnscaledTranscodeUsesSourceResolution(t *testing.T) {
	variant := transcodeTestVariant(1000, 128, 0, 0, 30)

	_, resolution, _ := hlsVariantPlaybackQuality(variant, testSourceDetails())
	if resolution != "1920x1080" {
		t.Fatalf("resolution = %q, want source 1920x1080", resolution)
	}

	_, unknownResolution, _ := hlsVariantPlaybackQuality(variant, nil)
	if unknownResolution != "" {
		t.Fatalf("resolution = %q, want empty when source geometry is unknown", unknownResolution)
	}
}

func TestHLSVariantPlaybackQualityDerivesUnsetScaledDimension(t *testing.T) {
	tests := []struct {
		name   string
		width  int
		height int
		want   string
	}{
		{name: "width only", width: 1280, want: "1280x720"},
		{name: "width only odd aspect", width: 1000, want: "1000x562"},
		{name: "height only", height: 720, want: "1280x720"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			variant := transcodeTestVariant(1000, 128, test.width, test.height, 30)
			_, resolution, _ := hlsVariantPlaybackQuality(variant, testSourceDetails())
			if resolution != test.want {
				t.Fatalf("resolution = %q, want %q", resolution, test.want)
			}
		})
	}
}

func TestHLSVariantPlaybackQualityAudioPassthroughAddsSourceAudio(t *testing.T) {
	variant := transcodeTestVariant(900, 0, 1280, 720, 30)
	if !variant.GetIsAudioPassthrough() {
		t.Fatal("expected an AudioBitrate=0 variant to passthrough audio")
	}

	bitrate, _, _ := hlsVariantPlaybackQuality(variant, testSourceDetails())
	requireBitrateKbps(t, bitrate, 1060)
}

func TestHLSVariantPlaybackQualityCopyUsesSource(t *testing.T) {
	bitrate, resolution, fps := hlsVariantPlaybackQuality(passthroughTestVariant(), testSourceDetails())
	requireBitrateKbps(t, bitrate, 4660)
	if resolution != "1920x1080" {
		t.Fatalf("resolution = %q, want 1920x1080", resolution)
	}
	requireFPS(t, fps, 30)

	// Without source metadata a copy variant cannot report quality: it must
	// not invent encoder settings it does not have.
	unknownBitrate, unknownResolution, unknownFPS := hlsVariantPlaybackQuality(passthroughTestVariant(), nil)
	if unknownBitrate != nil || unknownResolution != "" || unknownFPS != nil {
		t.Fatalf("copy variant invented quality without a source: %v %q %v", unknownBitrate, unknownResolution, unknownFPS)
	}
}

func TestHLSVariantPlaybackQualityHLSModeCopyUsesSource(t *testing.T) {
	variant := transcodeTestVariant(1000, 128, 1280, 720, 30)
	variant.Outputs = &models.VariantOutputs{
		HLS: &models.ProtocolOutputSettings{Mode: models.OutputModeCopy},
	}

	bitrate, resolution, _ := hlsVariantPlaybackQuality(variant, testSourceDetails())
	requireBitrateKbps(t, bitrate, 4660)
	if resolution != "1920x1080" {
		t.Fatalf("resolution = %q, want source resolution for an HLS copy variant", resolution)
	}
}

func TestSetViewerActiveWithVariantTracksQualityAndABRSwitch(t *testing.T) {
	variants := []models.StreamOutputVariant{
		transcodeTestVariant(1000, 128, 1280, 720, 30),
		passthroughTestVariant(),
	}
	service := newViewerStatsService(t, variants, &models.Broadcaster{StreamDetails: *testSourceDetails()})

	// Master playlist request: no variant, so quality stays unknown.
	service.SetViewerActive(newHLSViewer("master-only"))
	requireNoQuality(t, storedViewer(t, service, "master-only"))

	// Variant playlist request: quality is derived from that variant.
	service.SetViewerActiveWithVariant(newHLSViewer("viewer-1"), 0)
	viewer := storedViewer(t, service, "viewer-1")
	if viewer.VariantIndex == nil || *viewer.VariantIndex != 0 {
		t.Fatalf("variant index = %v, want 0", viewer.VariantIndex)
	}
	if viewer.VariantName != "720p30" {
		t.Fatalf("variant name = %q, want 720p30", viewer.VariantName)
	}
	requireBitrateKbps(t, viewer.BitrateKbps, 1128)
	if viewer.Resolution != "1280x720" {
		t.Fatalf("resolution = %q, want 1280x720", viewer.Resolution)
	}
	requireFPS(t, viewer.FPS, 30)

	// ABR switch: the latest requested variant wins.
	service.SetViewerActiveWithVariant(newHLSViewer("viewer-1"), 1)
	viewer = storedViewer(t, service, "viewer-1")
	if viewer.VariantIndex == nil || *viewer.VariantIndex != 1 {
		t.Fatalf("variant index after switch = %v, want 1", viewer.VariantIndex)
	}
	requireBitrateKbps(t, viewer.BitrateKbps, 4660)
	if viewer.Resolution != "1920x1080" {
		t.Fatalf("resolution after switch = %q, want 1920x1080", viewer.Resolution)
	}
	requireFPS(t, viewer.FPS, 30)

	// A later ping/master request must not erase the observed quality.
	service.SetViewerActive(newHLSViewer("viewer-1"))
	viewer = storedViewer(t, service, "viewer-1")
	if viewer.VariantIndex == nil || *viewer.VariantIndex != 1 {
		t.Fatalf("variant index after variant-less request = %v, want 1", viewer.VariantIndex)
	}
	requireBitrateKbps(t, viewer.BitrateKbps, 4660)
}

func TestSetViewerActiveWithVariantIgnoresUnknownOrNonHLSVariant(t *testing.T) {
	hlsDisabled := transcodeTestVariant(1000, 128, 1280, 720, 30)
	hlsDisabled.Outputs = &models.VariantOutputs{HLS: &models.ProtocolOutputSettings{Enabled: models.BoolPtr(false)}}
	// Use the broadcast snapshot: the persisted configuration is gob-encoded
	// and cannot represent a false Enabled pointer here, while the per-stream
	// snapshot is the plan actually being served.
	service := newViewerStatsService(t, nil, nil)
	service.currentBroadcast = &models.CurrentBroadcast{
		OutputSettings: []models.StreamOutputVariant{hlsDisabled},
	}

	service.SetViewerActiveWithVariant(newHLSViewer("bad-index"), 9)
	requireNoQuality(t, storedViewer(t, service, "bad-index"))

	service.SetViewerActiveWithVariant(newHLSViewer("hls-disabled"), 0)
	requireNoQuality(t, storedViewer(t, service, "hls-disabled"))
}

func TestActiveOutputVariantsPrefersBroadcastSnapshot(t *testing.T) {
	service := newViewerStatsService(t, []models.StreamOutputVariant{
		transcodeTestVariant(1000, 128, 1280, 720, 30),
	}, nil)
	// The snapshot captured at stream start wins over persisted configuration
	// so a config edit mid-stream cannot mislabel what the viewer receives.
	service.currentBroadcast = &models.CurrentBroadcast{
		OutputSettings: []models.StreamOutputVariant{passthroughTestVariant()},
	}

	service.SetViewerActiveWithVariant(newHLSViewer("snapshot"), 0)
	viewer := storedViewer(t, service, "snapshot")
	if viewer.VariantIndex == nil || *viewer.VariantIndex != 0 {
		t.Fatalf("variant index = %v, want 0", viewer.VariantIndex)
	}
	if viewer.VariantName != "copy" {
		t.Fatalf("variant name = %q, want the broadcast snapshot variant", viewer.VariantName)
	}
}

func TestEvenRound(t *testing.T) {
	tests := map[float64]int{
		720:    720,
		562.5:  562,
		563.4:  564,
		1000.6: 1000,
		0:      0,
	}
	for input, want := range tests {
		if got := evenRound(input); got != want {
			t.Fatalf("evenRound(%.1f) = %d, want %d", input, got, want)
		}
	}
}

func TestSaveStatsPersistsRecentLastLiveHeartbeatWhileOnline(t *testing.T) {
	repo := newStreamStatsTestRepo(t)
	connectedAt := time.Now().Add(-3 * time.Hour)
	s := &Service{
		configRepository: repo,
		stats: &models.Stats{
			StreamConnected: true,
			LastConnectTime: &utils.NullTime{Time: connectedAt, Valid: true},
		},
	}

	beforeSave := time.Now().Add(-2 * time.Second)
	s.saveStats()

	got, err := repo.GetLastDisconnectTime()
	if err != nil {
		t.Fatalf("GetLastDisconnectTime: %v", err)
	}
	if got == nil || !got.Valid {
		t.Fatal("expected an online heartbeat timestamp to be persisted")
	}
	if got.Time.Before(beforeSave) {
		t.Fatalf("persisted timestamp %v is older than save window start %v", got.Time, beforeSave)
	}
	if got.Time.Before(connectedAt) {
		t.Fatalf("persisted timestamp %v should not predate the live session start %v", got.Time, connectedAt)
	}

	restored := (&Service{configRepository: repo}).getSavedStats()
	if restored.LastDisconnectTime == nil || !restored.LastDisconnectTime.Valid {
		t.Fatal("expected restored stats to include the persisted last-live timestamp")
	}
	if restored.LastDisconnectTime.Time.Before(beforeSave) {
		t.Fatalf("restored timestamp %v is older than save window start %v", restored.LastDisconnectTime.Time, beforeSave)
	}
}

func TestSaveStatsPersistsExplicitDisconnectTimeWhenOffline(t *testing.T) {
	repo := newStreamStatsTestRepo(t)
	disconnectedAt := time.Now().Add(-45 * time.Minute).Round(time.Second)
	s := &Service{
		configRepository: repo,
		stats: &models.Stats{
			StreamConnected:    false,
			LastDisconnectTime: &utils.NullTime{Time: disconnectedAt, Valid: true},
		},
	}

	s.saveStats()

	got, err := repo.GetLastDisconnectTime()
	if err != nil {
		t.Fatalf("GetLastDisconnectTime: %v", err)
	}
	if got == nil || !got.Valid {
		t.Fatal("expected a disconnect timestamp to be persisted")
	}
	if !got.Time.Equal(disconnectedAt) {
		t.Fatalf("persisted disconnect time = %v, want %v", got.Time, disconnectedAt)
	}
}
