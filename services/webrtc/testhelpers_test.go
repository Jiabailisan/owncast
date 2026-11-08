package webrtc

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/owncast/owncast/models"
	"github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/require"
)

// testBrowserOffer creates a browser-like receiver PeerConnection and returns
// its offer SDP without waiting for ICE gathering (the server answer does not
// need the viewer's candidates to be complete).
func testBrowserOffer(t *testing.T) (*webrtc.PeerConnection, string) {
	t.Helper()

	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = pc.Close() })

	for _, kind := range []webrtc.RTPCodecType{webrtc.RTPCodecTypeVideo, webrtc.RTPCodecTypeAudio} {
		_, err := pc.AddTransceiverFromKind(kind, webrtc.RTPTransceiverInit{
			Direction: webrtc.RTPTransceiverDirectionRecvonly,
		})
		require.NoError(t, err)
	}

	offer, err := pc.CreateOffer(nil)
	require.NoError(t, err)
	require.NoError(t, pc.SetLocalDescription(offer))
	require.NotNil(t, pc.LocalDescription())

	return pc, pc.LocalDescription().SDP
}

// testBrowserOfferWithCandidates is the non-trickle variant used by the
// end-to-end media test: the offer contains the viewer's candidates.
func testBrowserOfferWithCandidates(t *testing.T) (*webrtc.PeerConnection, string) {
	t.Helper()

	pc, _ := testBrowserOffer(t)
	gather := webrtc.GatheringCompletePromise(pc)
	select {
	case <-gather:
	case <-time.After(5 * time.Second):
		t.Fatal("client ICE gathering timed out")
	}

	return pc, pc.LocalDescription().SDP
}

func testWebRTCConfig(mutate func(*models.WebRTCConfig)) models.WebRTCConfig {
	cfg := models.DefaultWebRTCConfig()
	cfg.Enabled = true
	cfg.BindAddress = "127.0.0.1"
	cfg.UDPMode = "range"
	cfg.UDPPortMin = 51000
	cfg.UDPPortMax = 51999
	cfg.TCPEnabled = false
	cfg.NetworkTypes = []string{"udp4"}
	cfg.STUNServers = []string{}
	cfg.TURNServers = []models.TURNServer{}
	cfg.EmbeddedTurn = false
	cfg.Domain = ""
	cfg.StrictDomain = false
	cfg.AllowedOrigins = []string{}
	cfg.SignalingRateLimit = 30
	cfg.MaxViewers = 100
	cfg.MaxViewersPerIP = 0
	cfg.H264Profile = "constrained-baseline"
	if mutate != nil {
		mutate(&cfg)
	}

	return cfg
}

func testService(t *testing.T, mutate func(*models.WebRTCConfig)) *Service {
	t.Helper()

	svc := New(Deps{Config: testWebRTCConfig(mutate)})
	require.NoError(t, svc.Start(context.Background()))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, svc.Stop(ctx))
	})

	return svc
}

func testWebRTCVariants(count int) []models.StreamOutputVariant {
	variants := make([]models.StreamOutputVariant, 0, count)
	for i := 0; i < count; i++ {
		variants = append(variants, models.StreamOutputVariant{
			Name:         "variant-" + string(rune('a'+i)),
			ScaledWidth:  1280,
			ScaledHeight: 720,
			Framerate:    30,
			Outputs: &models.VariantOutputs{
				WebRTC: &models.ProtocolOutputSettings{Enabled: models.BoolPtr(true)},
			},
		})
	}

	return variants
}

func countOccurrences(haystack, needle string) int {
	return strings.Count(haystack, needle)
}
