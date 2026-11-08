package configrepository

import (
	"reflect"
	"testing"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/datastore"
)

// newPhase1TestRepo returns an in-memory config repository plus its backing
// datastore so tests can also write raw values (to simulate stale/corrupt
// stored configurations).
func newPhase1TestRepo(t *testing.T) (ConfigRepository, *datastore.Datastore) {
	t.Helper()
	ds, err := datastore.SetupPersistence(":memory:", t.TempDir())
	if err != nil {
		t.Fatalf("failed to set up datastore: %v", err)
	}
	return New(ds), ds
}

func mustNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertEqual[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

func assertDeepEqual(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("values are not deeply equal:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestSRTConfigDefaultsWhenUnset(t *testing.T) {
	repo, _ := newPhase1TestRepo(t)
	assertDeepEqual(t, repo.GetSRTConfig(), models.DefaultSRTConfig())
}

func TestWebRTCConfigDefaultsWhenUnset(t *testing.T) {
	repo, _ := newPhase1TestRepo(t)
	assertDeepEqual(t, repo.GetWebRTCConfig(), models.DefaultWebRTCConfig())
}

func TestSRTConfigRoundTrip(t *testing.T) {
	repo, _ := newPhase1TestRepo(t)

	config := models.DefaultSRTConfig()
	config.Enabled = true
	config.BindAddress = "127.0.0.1"
	config.Port = 9000
	config.StreamIDFormats = []string{string(models.SRTStreamIDRawKey)}
	config.RequireStreamID = false
	config.AllowedCIDRs = []string{"10.0.0.0/8", "2001:db8::/32"}
	config.DeniedCIDRs = []string{"192.0.2.0/24"}
	config.AuthFailureRateLimit = 0
	config.ReceiverLatencyMs = 300
	config.PeerLatencyMs = 240
	config.FlowControlWindowPackets = 51200
	config.MSS = 1456
	config.PayloadSize = 1316
	config.Passphrase = "correct-horse-battery"
	config.EnforcedEncryption = false
	config.PeerIdleTimeoutMs = 3000
	config.IPTTL = 64
	config.IPTOS = 16
	config.LogTopics = []string{"listen", "connection"}
	config.DebugStatsIntervalMs = 5000
	config.DriftTracer = false
	config.MaxPendingHandshakes = 8

	mustNoError(t, repo.SetSRTConfig(config))
	// The secret is persisted like every other field; the admin API is what
	// masks it (config/settings marks it Secret).
	assertDeepEqual(t, repo.GetSRTConfig(), config)
}

func TestWebRTCConfigRoundTrip(t *testing.T) {
	repo, _ := newPhase1TestRepo(t)

	config := models.DefaultWebRTCConfig()
	config.Enabled = true
	config.Domain = "live.example.com"
	config.PublicIPs = []string{"203.0.113.5"}
	config.StrictDomain = true
	config.AllowedOrigins = []string{"https://example.com"}
	config.MaxViewers = 250
	config.MaxViewersPerIP = 2
	config.SignalingRateLimit = 60
	config.SessionIdleTimeoutSeconds = 45
	config.UDPMode = "range"
	config.UDPPortMin = 50000
	config.UDPPortMax = 50200
	config.NetworkTypes = []string{"udp4", "udp6"}
	config.STUNServers = []string{"stun:stun.example.org:3478"}
	config.TURNServers = []models.TURNServer{
		{URL: "turn:turn.example.org:3478", Username: "viewer", Credential: "super-secret-credential"},
		{URL: "turns:turn.example.org:5349", StaticSecret: "another-super-secret"},
	}
	config.EmbeddedTurnRealm = "example"
	config.H264Profile = "main"
	config.H264Level = "4.1"
	config.KeyframeIntervalSeconds = 2
	config.OpusBitrateKbps = 128
	config.OpusFrameMs = 40
	config.OpusFEC = false
	config.RTPPacketSize = 1100
	config.MaxBitratePerViewerKbps = 2500
	config.MinBitratePerViewerKbps = 500
	config.RTPReorderBufferMs = 100

	mustNoError(t, repo.SetWebRTCConfig(config))
	assertDeepEqual(t, repo.GetWebRTCConfig(), config)
}

func TestSRTConfigDecodeFailureFallsBackToDefaults(t *testing.T) {
	repo, ds := newPhase1TestRepo(t)

	// A gob-encoded string cannot decode into models.SRTConfig: this is the
	// "stale/corrupt stored value" case.
	mustNoError(t, ds.SetString(srtConfigKey, "not-an-srt-config"))
	assertDeepEqual(t, repo.GetSRTConfig(), models.DefaultSRTConfig())
}

func TestWebRTCConfigDecodeFailureFallsBackToDefaults(t *testing.T) {
	repo, ds := newPhase1TestRepo(t)

	mustNoError(t, ds.SetString(webrtcConfigKey, "not-a-webrtc-config"))
	assertDeepEqual(t, repo.GetWebRTCConfig(), models.DefaultWebRTCConfig())
}

func TestSRTConfigStaleZerosAreNormalized(t *testing.T) {
	repo, ds := newPhase1TestRepo(t)

	// A config written before a field existed decodes as the zero value.
	mustNoError(t, ds.Save(models.ConfigEntry{Key: srtConfigKey, Value: models.SRTConfig{}}))
	stored := repo.GetSRTConfig()
	assertEqual(t, stored.BindAddress, "0.0.0.0")
	assertEqual(t, stored.Port, 8890)
	assertEqual(t, len(stored.StreamIDFormats), len(models.AllSRTStreamIDFormats()))
	assertEqual(t, stored.ReceiverLatencyMs, 120)
	assertEqual(t, stored.MSS, 1500)
	assertEqual(t, stored.PayloadSize, 1316)
	assertEqual(t, stored.PeerIdleTimeoutMs, 5000)
	assertEqual(t, stored.MaxPendingHandshakes, 32)
	// Explicitly legal zero values are preserved, not overwritten with the
	// model defaults.
	assertEqual(t, stored.AuthFailureRateLimit, 0)
	assertEqual(t, stored.DebugStatsIntervalMs, 0)
	assertEqual(t, stored.IPTTL, 0)
	assertEqual(t, stored.IPTOS, 0)
}

func TestWebRTCConfigStaleZerosAreNormalized(t *testing.T) {
	repo, ds := newPhase1TestRepo(t)

	mustNoError(t, ds.Save(models.ConfigEntry{Key: webrtcConfigKey, Value: models.WebRTCConfig{}}))
	stored := repo.GetWebRTCConfig()
	assertEqual(t, stored.BindAddress, "0.0.0.0")
	assertEqual(t, stored.UDPMode, "mux")
	assertEqual(t, stored.UDPPort, 8189)
	assertEqual(t, stored.UDPPortMin, 50000)
	assertEqual(t, stored.UDPPortMax, 50100)
	assertEqual(t, stored.TCPPort, 8189)
	assertEqual(t, stored.LimitExceededBehavior, "reject")
	assertEqual(t, stored.SignalingRateLimit, 30)
	assertEqual(t, stored.SessionIdleTimeoutSeconds, 30)
	assertEqual(t, stored.H264Profile, "constrained-baseline")
	assertEqual(t, stored.H264Level, "auto")
	assertEqual(t, stored.KeyframeIntervalSeconds, 1.0)
	assertEqual(t, stored.OpusBitrateKbps, 96)
	assertEqual(t, stored.OpusFrameMs, 20)
	assertEqual(t, stored.RTPPacketSize, 1200)
	if len(stored.NetworkTypes) == 0 {
		t.Error("expected normalized network types")
	}
	// Legal "off"/unlimited zeros are preserved.
	assertEqual(t, stored.MaxViewers, 0)
	assertEqual(t, stored.MaxViewersPerIP, 0)
	assertEqual(t, stored.MaxBitratePerViewerKbps, 0)
	assertEqual(t, stored.MinBitratePerViewerKbps, 0)
	assertEqual(t, stored.RTPReorderBufferMs, 0)
}

func TestCustomAppearancePathsRoundTrip(t *testing.T) {
	repo, _ := newPhase1TestRepo(t)

	assertEqual(t, repo.GetCustomPlayerPath(), "")
	assertEqual(t, repo.GetCustomFrontendDir(), "")

	mustNoError(t, repo.SetCustomPlayerPath("/srv/owncast/player.js"))
	mustNoError(t, repo.SetCustomFrontendDir("/srv/owncast/frontend"))
	assertEqual(t, repo.GetCustomPlayerPath(), "/srv/owncast/player.js")
	assertEqual(t, repo.GetCustomFrontendDir(), "/srv/owncast/frontend")
}

func TestOutputProcessModelDefaultAndRoundTrip(t *testing.T) {
	repo, _ := newPhase1TestRepo(t)

	assertEqual(t, repo.GetOutputProcessModel(), models.OutputProcessModelShared)
	mustNoError(t, repo.SetOutputProcessModel(models.OutputProcessModelIsolated))
	assertEqual(t, repo.GetOutputProcessModel(), models.OutputProcessModelIsolated)
	mustNoError(t, repo.SetOutputProcessModel(models.OutputProcessModelShared))
	assertEqual(t, repo.GetOutputProcessModel(), models.OutputProcessModelShared)
}

func TestOutputProcessModelRejectsUnknownValue(t *testing.T) {
	repo, _ := newPhase1TestRepo(t)
	if err := repo.SetOutputProcessModel("something-else"); err == nil {
		t.Fatal("expected an error for an unknown process model")
	}
	assertEqual(t, repo.GetOutputProcessModel(), models.OutputProcessModelShared)
}

func TestOutputProcessModelStoredUnknownValueFallsBackToShared(t *testing.T) {
	repo, ds := newPhase1TestRepo(t)
	mustNoError(t, ds.SetString(outputProcessModelKey, "legacy-value"))
	assertEqual(t, repo.GetOutputProcessModel(), models.OutputProcessModelShared)
}

// Ensure the repository satisfies the interface at compile time (it is also
// asserted by engineConfig.go); this keeps the Phase 1 methods covered.
var _ ConfigRepository = (*SqlConfigRepository)(nil)
