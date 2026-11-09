package webrtc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/owncast/owncast/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func whepRequest(t *testing.T, svc *Service, method, target, contentType, body string, mutate func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if mutate != nil {
		mutate(req)
	}
	recorder := httptest.NewRecorder()
	svc.Handler().ServeHTTP(recorder, req)

	return recorder
}

func decodeBody(t *testing.T, recorder *httptest.ResponseRecorder) map[string]string {
	t.Helper()

	require.NotEmpty(t, recorder.Body.String())
	payload := map[string]string{}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))

	return payload
}

func TestWHEPPostCreatesSession(t *testing.T) {
	svc := testService(t, nil)
	require.NoError(t, svc.SetVariants(testWebRTCVariants(1)))

	_, offer := testBrowserOffer(t)
	recorder := whepRequest(t, svc, http.MethodPost, "/webrtc/whep", "application/sdp", offer, nil)

	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	assert.Equal(t, "application/sdp", recorder.Header().Get("Content-Type"))
	location := recorder.Header().Get("Location")
	assert.True(t, strings.HasPrefix(location, "/webrtc/whep/session/"))
	assert.NotEmpty(t, recorder.Header().Get("ETag"))
	answer := recorder.Body.String()
	assert.Contains(t, answer, "m=video")
	assert.Contains(t, answer, "m=audio")
	assert.Equal(t, 1, svc.ViewerCount())

	capacity := svc.Capacity()
	assert.Equal(t, 1, capacity.Used)
	assert.Equal(t, 100, capacity.Limit)
}

func TestWHEPDeleteIsIdempotent(t *testing.T) {
	svc := testService(t, nil)
	require.NoError(t, svc.SetVariants(testWebRTCVariants(1)))

	_, offer := testBrowserOffer(t)
	created := whepRequest(t, svc, http.MethodPost, "/webrtc/whep", "application/sdp", offer, nil)
	require.Equal(t, http.StatusCreated, created.Code)

	location := created.Header().Get("Location")
	firstDelete := whepRequest(t, svc, http.MethodDelete, location, "", "", nil)
	assert.Equal(t, http.StatusNoContent, firstDelete.Code)
	assert.Equal(t, 0, svc.ViewerCount())

	secondDelete := whepRequest(t, svc, http.MethodDelete, location, "", "", nil)
	assert.Equal(t, http.StatusNoContent, secondDelete.Code)
}

func TestWHEPPatchTrickle(t *testing.T) {
	svc := testService(t, nil)
	require.NoError(t, svc.SetVariants(testWebRTCVariants(1)))

	_, offer := testBrowserOffer(t)
	created := whepRequest(t, svc, http.MethodPost, "/webrtc/whep", "application/sdp", offer, nil)
	require.Equal(t, http.StatusCreated, created.Code)
	location := created.Header().Get("Location")

	fragment := "a=ice-ufrag:abcd\r\na=ice-pwd:secret\r\nm=video 9 UDP/TLS/RTP/SAVPF 96\r\na=mid:0\r\na=candidate:1 1 udp 2122260223 203.0.113.7 51000 typ host\r\n"
	patched := whepRequest(t, svc, http.MethodPatch, location, trickleContentType, fragment, nil)
	assert.Equal(t, http.StatusNoContent, patched.Code, patched.Body.String())

	wrongContentType := whepRequest(t, svc, http.MethodPatch, location, "application/sdp", fragment, nil)
	assert.Equal(t, http.StatusUnsupportedMediaType, wrongContentType.Code)

	unknown := whepRequest(t, svc, http.MethodPatch, "/webrtc/whep/session/does-not-exist", trickleContentType, fragment, nil)
	assert.Equal(t, http.StatusNotFound, unknown.Code)
}

func TestWHEPDisabled(t *testing.T) {
	svc := testService(t, func(cfg *models.WebRTCConfig) { cfg.Enabled = false })
	require.NoError(t, svc.SetVariants(testWebRTCVariants(1)))

	_, offer := testBrowserOffer(t)
	recorder := whepRequest(t, svc, http.MethodPost, "/webrtc/whep", "application/sdp", offer, nil)
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	assert.Equal(t, CodeDisabled, decodeBody(t, recorder)["code"])
}

func TestWHEPBadRequests(t *testing.T) {
	svc := testService(t, nil)
	require.NoError(t, svc.SetVariants(testWebRTCVariants(1)))

	malformed := whepRequest(t, svc, http.MethodPost, "/webrtc/whep", "application/sdp", "this is not sdp", nil)
	assert.Equal(t, http.StatusBadRequest, malformed.Code)
	assert.Equal(t, CodeBadOffer, decodeBody(t, malformed)["code"])

	_, offer := testBrowserOffer(t)
	badType := whepRequest(t, svc, http.MethodPost, "/webrtc/whep", "application/json", offer, nil)
	assert.Equal(t, http.StatusUnsupportedMediaType, badType.Code)

	get := whepRequest(t, svc, http.MethodGet, "/webrtc/whep", "", "", nil)
	assert.Equal(t, http.StatusMethodNotAllowed, get.Code)
	assert.NotEmpty(t, get.Header().Get("Allow"))

	unknownVariant := whepRequest(t, svc, http.MethodPost, "/webrtc/whep?variant=9", "application/sdp", offer, nil)
	assert.Equal(t, http.StatusBadRequest, unknownVariant.Code)
	assert.Equal(t, CodeVariantNotFound, decodeBody(t, unknownVariant)["code"])
}

func TestWHEPCapacityGate(t *testing.T) {
	svc := testService(t, func(cfg *models.WebRTCConfig) { cfg.MaxViewers = 1 })
	require.NoError(t, svc.SetVariants(testWebRTCVariants(1)))

	_, offer := testBrowserOffer(t)
	first := whepRequest(t, svc, http.MethodPost, "/webrtc/whep", "application/sdp", offer, nil)
	require.Equal(t, http.StatusCreated, first.Code, first.Body.String())

	second := whepRequest(t, svc, http.MethodPost, "/webrtc/whep", "application/sdp", offer, nil)
	require.Equal(t, http.StatusServiceUnavailable, second.Code)
	assert.Equal(t, CodeCapacity, decodeBody(t, second)["code"])
	assert.NotEmpty(t, second.Header().Get("Retry-After"))
}

func TestWHEPSignalingRateLimit(t *testing.T) {
	svc := testService(t, func(cfg *models.WebRTCConfig) { cfg.SignalingRateLimit = 1 })
	require.NoError(t, svc.SetVariants(testWebRTCVariants(1)))

	_, offer := testBrowserOffer(t)
	first := whepRequest(t, svc, http.MethodPost, "/webrtc/whep", "application/sdp", offer, nil)
	require.Equal(t, http.StatusCreated, first.Code, first.Body.String())

	second := whepRequest(t, svc, http.MethodPost, "/webrtc/whep", "application/sdp", offer, nil)
	require.Equal(t, http.StatusTooManyRequests, second.Code)
	assert.Equal(t, CodeRateLimited, decodeBody(t, second)["code"])
}

func TestWHEPStrictDomain(t *testing.T) {
	svc := testService(t, func(cfg *models.WebRTCConfig) {
		cfg.Domain = "live.example.com"
		cfg.StrictDomain = true
	})
	require.NoError(t, svc.SetVariants(testWebRTCVariants(1)))

	_, offer := testBrowserOffer(t)
	mismatched := whepRequest(t, svc, http.MethodPost, "/webrtc/whep", "application/sdp", offer, nil)
	require.Equal(t, http.StatusForbidden, mismatched.Code)
	assert.Equal(t, CodeDomainMismatch, decodeBody(t, mismatched)["code"])

	allowed := whepRequest(t, svc, http.MethodPost, "/webrtc/whep", "application/sdp", offer, func(req *http.Request) {
		req.Host = "live.example.com"
		req.Header.Set("Origin", "https://live.example.com")
	})
	assert.Equal(t, http.StatusCreated, allowed.Code, allowed.Body.String())
}

func TestWHEPSameOriginAcceptedByDefault(t *testing.T) {
	// Regression test for the browser integration bug: with the default
	// configuration (no domain, strictDomain=false, empty allowedOrigins) the
	// same-origin viewer page must be able to POST its offer.
	svc := testService(t, nil)
	require.NoError(t, svc.SetVariants(testWebRTCVariants(1)))

	_, offer := testBrowserOffer(t)
	sameOrigin := whepRequest(t, svc, http.MethodPost, "/webrtc/whep", "application/sdp", offer, func(req *http.Request) {
		req.Host = "127.0.0.1:18080"
		req.Header.Set("Origin", "http://127.0.0.1:18080")
	})
	require.Equal(t, http.StatusCreated, sameOrigin.Code, sameOrigin.Body.String())
	assert.Empty(t, sameOrigin.Header().Get("Access-Control-Allow-Origin"), "same-origin needs no CORS headers")

	crossOrigin := whepRequest(t, svc, http.MethodPost, "/webrtc/whep", "application/sdp", offer, func(req *http.Request) {
		req.Host = "127.0.0.1:18080"
		req.Header.Set("Origin", "http://evil.example.com")
	})
	require.Equal(t, http.StatusForbidden, crossOrigin.Code)
	assert.Equal(t, CodeDomainMismatch, decodeBody(t, crossOrigin)["code"])
}

func TestWHEPAllowedOriginsPermitCrossOrigin(t *testing.T) {
	svc := testService(t, func(cfg *models.WebRTCConfig) {
		cfg.AllowedOrigins = []string{"http://embed.example.com"}
	})
	require.NoError(t, svc.SetVariants(testWebRTCVariants(1)))

	_, offer := testBrowserOffer(t)
	allowed := whepRequest(t, svc, http.MethodPost, "/webrtc/whep", "application/sdp", offer, func(req *http.Request) {
		req.Host = "127.0.0.1:18080"
		req.Header.Set("Origin", "http://embed.example.com")
	})
	require.Equal(t, http.StatusCreated, allowed.Code, allowed.Body.String())
	assert.Equal(t, "http://embed.example.com", allowed.Header().Get("Access-Control-Allow-Origin"))
}

func TestWHEPCORS(t *testing.T) {
	svc := testService(t, func(cfg *models.WebRTCConfig) {
		cfg.AllowedOrigins = []string{"https://embed.example.com"}
	})
	require.NoError(t, svc.SetVariants(testWebRTCVariants(1)))

	preflight := whepRequest(t, svc, http.MethodOptions, "/webrtc/whep", "", "", func(req *http.Request) {
		req.Header.Set("Origin", "https://embed.example.com")
		req.Header.Set("Access-Control-Request-Method", "POST")
	})
	require.Equal(t, http.StatusNoContent, preflight.Code)
	assert.Equal(t, "https://embed.example.com", preflight.Header().Get("Access-Control-Allow-Origin"))
	assert.Contains(t, preflight.Header().Get("Access-Control-Allow-Methods"), "POST")
	assert.Equal(t, "application/sdp", preflight.Header().Get("Accept-Post"))

	_, offer := testBrowserOffer(t)
	blocked := whepRequest(t, svc, http.MethodPost, "/webrtc/whep", "application/sdp", offer, func(req *http.Request) {
		req.Header.Set("Origin", "https://evil.example.com")
	})
	assert.Equal(t, http.StatusForbidden, blocked.Code)

	allowed := whepRequest(t, svc, http.MethodPost, "/webrtc/whep", "application/sdp", offer, func(req *http.Request) {
		req.Header.Set("Origin", "https://embed.example.com")
	})
	require.Equal(t, http.StatusCreated, allowed.Code, allowed.Body.String())
	assert.Equal(t, "https://embed.example.com", allowed.Header().Get("Access-Control-Allow-Origin"))
}

func TestWHEPLinkHeaders(t *testing.T) {
	// Viewer-facing TURN only: server-side STUN would make the answer wait for
	// an unreachable STUN server in the test sandbox.
	svc := testService(t, func(cfg *models.WebRTCConfig) {
		cfg.TURNServers = []models.TURNServer{{URL: "turn:turn.example.org:3478?transport=udp", Username: "u", Credential: "c"}}
	})
	require.NoError(t, svc.SetVariants(testWebRTCVariants(1)))

	_, offer := testBrowserOffer(t)
	recorder := whepRequest(t, svc, http.MethodPost, "/webrtc/whep", "application/sdp", offer, nil)
	require.Equal(t, http.StatusCreated, recorder.Code)
	links := recorder.Header().Values("Link")
	require.Len(t, links, 1)
	assert.Contains(t, links[0], "turn:turn.example.org:3478")
	assert.Contains(t, links[0], "credential=\"c\"")
}

func TestWHEPClientReportAndLiveViewers(t *testing.T) {
	svc := testService(t, nil)
	require.NoError(t, svc.SetVariants(testWebRTCVariants(0)))

	viewerID := "0123456789abcdef0123456789abcdef"
	assert.False(t, svc.ApplyClientReport(viewerID, ClientReport{FPS: 30}))

	// Create a real session so the registry has a row to enrich.
	require.NoError(t, svc.SetVariants(testWebRTCVariants(1)))
	_, offer := testBrowserOffer(t)
	created := whepRequest(t, svc, http.MethodPost, "/webrtc/whep", "application/sdp", offer, nil)
	require.Equal(t, http.StatusCreated, created.Code)
	location := created.Header().Get("Location")
	id := strings.TrimPrefix(location, "/webrtc/whep/session/")

	packetLoss := 1.42
	clientBitrate := 5980.0
	assert.True(t, svc.ApplyClientReport(id, ClientReport{
		DecodedWidth:     2560,
		DecodedHeight:    1440,
		FPS:              52.4,
		PacketsLost:      126,
		PacketsReceived:  8763,
		PacketLossPct:    &packetLoss,
		JitterMs:         4.1,
		Transport:        "udp",
		VideoBitrateKbps: &clientBitrate,
	}))
	viewers := svc.LiveViewers()
	require.Len(t, viewers, 1)
	assert.Equal(t, "webrtc", viewers[0].Protocol)
	assert.Equal(t, "client", viewers[0].Source)
	assert.Equal(t, "2560x1440", viewers[0].Resolution)
	require.NotNil(t, viewers[0].FPS)
	assert.InDelta(t, 52.4, *viewers[0].FPS, 0.001)
	require.NotNil(t, viewers[0].BitrateKbps)
	assert.InDelta(t, 5980, *viewers[0].BitrateKbps, 0.001)
	require.NotNil(t, viewers[0].PacketLossPct)
	assert.InDelta(t, 1.42, *viewers[0].PacketLossPct, 0.001)
	assert.Equal(t, "udp", viewers[0].ICETransport)

	// An unvalidated spike (571 fps) and a bogus resolution/loss are ignored;
	// only the usable transport fallback remains, so the row is no longer
	// labelled client-reported.
	assert.True(t, svc.ApplyClientReport(id, ClientReport{FPS: 571, DecodedWidth: 8, DecodedHeight: 8, Transport: "udp"}))
	viewers = svc.LiveViewers()
	require.Len(t, viewers, 1)
	assert.NotEqual(t, 571.0, viewers[0].FPS)
	assert.Equal(t, "udp", viewers[0].ICETransport)
	assert.Equal(t, "server", viewers[0].Source)

	snapshot := svc.Snapshot()
	assert.True(t, snapshot.Enabled)
	assert.Equal(t, 1, snapshot.ActiveSessions)
	assert.Contains(t, snapshot.Listening, "udp://127.0.0.1:")
}

func TestWHEPDisabledSnapshotAndWarnings(t *testing.T) {
	svc := testService(t, func(cfg *models.WebRTCConfig) { cfg.Enabled = false })
	_ = svc.SetVariants(testWebRTCVariants(1))

	snapshot := svc.Snapshot()
	assert.False(t, snapshot.Enabled)
	assert.Empty(t, snapshot.Listening)
	warnings := svc.Warnings()
	assert.NotEmpty(t, warnings)
}

func TestWHEPTestDomain(t *testing.T) {
	svc := testService(t, nil)

	result := svc.TestDomain(context.Background(), "localhost")
	assert.Empty(t, result.Error)
	assert.NotEmpty(t, result.ResolvedIPs)

	bad := svc.TestDomain(context.Background(), "not a valid domain")
	assert.NotEmpty(t, bad.Error)
}
