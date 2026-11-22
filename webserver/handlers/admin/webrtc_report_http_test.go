package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/livestatus"
	"github.com/owncast/owncast/services/webrtc"
	pionwebrtc "github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/require"
)

// reportTestConfig binds the WebRTC engine to loopback with a fixed UDP range so the test
// needs no interface enumeration.
func reportTestConfig() models.WebRTCConfig {
	cfg := models.DefaultWebRTCConfig()
	cfg.Enabled = true
	cfg.BindAddress = "127.0.0.1"
	cfg.UDPMode = "range"
	cfg.UDPPortMin = 52000
	cfg.UDPPortMax = 52999
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

	return cfg
}

func reportTestVariants() []models.StreamOutputVariant {
	return []models.StreamOutputVariant{{
		Name:         "variant-a",
		ScaledWidth:  1280,
		ScaledHeight: 720,
		Framerate:    30,
		Outputs: &models.VariantOutputs{
			WebRTC: &models.ProtocolOutputSettings{Enabled: models.BoolPtr(true)},
		},
	}}
}

func reportTestOffer(t *testing.T) string {
	t.Helper()

	pc, err := pionwebrtc.NewPeerConnection(pionwebrtc.Configuration{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = pc.Close() })

	for _, kind := range []pionwebrtc.RTPCodecType{pionwebrtc.RTPCodecTypeVideo, pionwebrtc.RTPCodecTypeAudio} {
		_, err := pc.AddTransceiverFromKind(kind, pionwebrtc.RTPTransceiverInit{
			Direction: pionwebrtc.RTPTransceiverDirectionRecvonly,
		})
		require.NoError(t, err)
	}

	offer, err := pc.CreateOffer(nil)
	require.NoError(t, err)
	require.NoError(t, pc.SetLocalDescription(offer))

	return pc.LocalDescription().SDP
}

// createReportSession opens a real WHEP session from the given address and returns its id.
func createReportSession(t *testing.T, svc *webrtc.Service, offer, remoteAddr string) string {
	t.Helper()

	request := httptest.NewRequest(http.MethodPost, "/webrtc/whep", strings.NewReader(offer))
	request.Header.Set("Content-Type", "application/sdp")
	request.RemoteAddr = remoteAddr
	recorder := httptest.NewRecorder()
	svc.Handler().ServeHTTP(recorder, request)
	require.Equal(t, http.StatusCreated, recorder.Code)

	return strings.TrimPrefix(recorder.Header().Get("Location"), "/webrtc/whep/session/")
}

type testNameProvider map[string]string

func (n testNameProvider) DisplayNameForToken(token string) string { return n[token] }

func (n testNameProvider) DisplayNamesForTokens(tokens []string) map[string]string {
	resolved := map[string]string{}
	for _, token := range tokens {
		if name, ok := n[token]; ok {
			resolved[token] = name
		}
	}

	return resolved
}

// Two sessions behind one public address must each show their own chat name and buffer
// delay, the reports must travel through the real stats handler, and clearing a token must
// drop the old name without touching the other session.
func TestReportHandlerFeedsTheViewerListPerSession(t *testing.T) {
	svc := webrtc.New(webrtc.Deps{Config: reportTestConfig()})
	require.NoError(t, svc.Start(context.Background()))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = svc.Stop(ctx)
	})
	generation, err := svc.SetVariants(reportTestVariants())
	require.NoError(t, err)
	// A variant is only deliverable once its media leg is open, which is what WHEP admits
	// viewers against.
	_, err = svc.OpenMediaLeg(0, generation)
	require.NoError(t, err)

	offer := reportTestOffer(t)
	idA := createReportSession(t, svc, offer, "198.51.100.40:40000")
	idB := createReportSession(t, svc, offer, "198.51.100.40:40001")
	require.NotEqual(t, idA, idB)

	admin := &Admin{
		webrtc: svc,
		liveStatus: livestatus.New(livestatus.Deps{
			WebRTC: svc,
			Names:  testNameProvider{"token-a": "alice", "token-b": "bob"},
		}),
	}

	report := func(sessionID string, token string, latency float64) {
		body := "{\"sessionId\":\"" + sessionID + "\",\"latencyMs\":" +
			strconv.FormatFloat(latency, 'f', -1, 64) + "}"
		request := httptest.NewRequest(http.MethodPost, "/api/admin/webrtc/client-report", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		if token != "" {
			request.Header.Set("X-Owncast-Access-Token", token)
		}
		recorder := httptest.NewRecorder()
		admin.ReportWebRTCClientReport(recorder, request)
		require.Equal(t, http.StatusNoContent, recorder.Code, recorder.Body.String())
	}

	report(idA, "token-a", 980)
	report(idB, "token-b", 1120)

	rows := liveViewerRows(t, admin)
	require.Equal(t, "alice", rows[idA].Username)
	require.Equal(t, "bob", rows[idB].Username)
	require.NotNil(t, rows[idA].LatencyMs)
	require.NotNil(t, rows[idB].LatencyMs)
	require.Equal(t, 980.0, *rows[idA].LatencyMs)
	require.Equal(t, 1120.0, *rows[idB].LatencyMs)

	// Session A drops its token and reports a new delay.
	report(idA, "", 1500)

	rows = liveViewerRows(t, admin)
	require.Empty(t, rows[idA].Username, "the old name must be gone once the token is cleared")
	require.NotNil(t, rows[idA].LatencyMs)
	require.Equal(t, 1500.0, *rows[idA].LatencyMs)
	require.Equal(t, "bob", rows[idB].Username, "the other session keeps its name")
	require.NotNil(t, rows[idB].LatencyMs)
	require.Equal(t, 1120.0, *rows[idB].LatencyMs)
}

// liveViewerRows asks the admin endpoint for the current list and keys the rows by session.
func liveViewerRows(t *testing.T, admin *Admin) map[string]models.LiveViewer {
	t.Helper()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/admin/live/viewers", nil)
	admin.GetLiveViewers(recorder, request, 1, 100, "connectedAt", "desc", "webrtc", "")

	var page models.LiveViewersPage
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &page))
	require.NotContains(t, recorder.Body.String(), "token-")

	rows := map[string]models.LiveViewer{}
	for _, row := range page.Viewers {
		rows[row.ClientID] = row
	}

	return rows
}
