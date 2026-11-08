//go:build integration

// Package webrtc_e2e is the live-server WebRTC/WHEP acceptance harness used by
// the automated QA stage. It is independent of the server process: QA starts a
// managed Owncast (with an RTMP and/or SRT publisher), then runs:
//
//	cd owncast && go test -tags=integration -count=1 -timeout 5m ./test/automated/webrtc/...
//
// Environment contract (all optional except the base URL):
//
//	OWNCAST_BASE_URL              default http://127.0.0.1:18081
//	OWNCAST_WHEP_VARIANT          WebRTC variant index to request (default 0)
//	OWNCAST_WHEP_EXPECT_LIVE      set to 1 with a live publisher to enable the
//	                              full WHEP -> ICE -> RTP media assertions (T3/T4)
//	OWNCAST_EXPECT_INPUT_PROTOCOL rtmp or srt; when set, the harness asserts
//	                              /api/status reports that input protocol
//	OWNCAST_WHEP_EXPECT_LINK      set to 1 with stunServers/turnServers configured
//	                              to require Link rel=ice-server headers
//	OWNCAST_WHEP_CAPACITY_TEST    set to 1 when the server runs with maxViewers=1
//	OWNCAST_WHEP_RATELIMIT_TEST   set to 1 when signalingRateLimit=1
//	OWNCAST_WHEP_STRICT_DOMAIN_TEST set to 1 when strictDomain=true; requires
//	                              OWNCAST_WHEP_DOMAIN for the matching Host
//
// Tests whose precondition cannot be observed over the public API are skipped
// with an explicit reason rather than faked: the QA runner maps SKIP honestly.
package webrtc_e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

const (
	defaultBaseURL = "http://127.0.0.1:18081"
	whepPath       = "/webrtc/whep"
	sessionPath    = "/webrtc/whep/session/"
	requestTimeout = 15 * time.Second
	connectTimeout = 20 * time.Second
	mediaTimeout   = 25 * time.Second
)

type harness struct {
	t       *testing.T
	baseURL string
	client  *http.Client
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("OWNCAST_BASE_URL")), "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	h := &harness{t: t, baseURL: baseURL, client: &http.Client{Timeout: requestTimeout}}
	h.requireWebRTCEnabled()

	return h
}

func (h *harness) getJSON(path string) (int, map[string]any) {
	h.t.Helper()

	response, err := h.client.Get(h.baseURL + path)
	if err != nil {
		h.t.Fatalf("GET %s: %v", path, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		h.t.Fatalf("read %s: %v", path, err)
	}
	payload := map[string]any{}
	if len(body) > 0 {
		_ = json.Unmarshal(body, &payload)
	}

	return response.StatusCode, payload
}

// requireWebRTCEnabled skips when no managed server is reachable or when its
// WebRTC delivery is disabled (the default), so the harness cannot produce
// false failures on a developer machine.
func (h *harness) requireWebRTCEnabled() {
	h.t.Helper()

	response, err := h.client.Get(h.baseURL + "/api/config")
	if err != nil {
		h.t.Skipf("owncast is not reachable at %s: %v", h.baseURL, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		h.t.Skipf("owncast /api/config returned %d at %s", response.StatusCode, h.baseURL)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		h.t.Skipf("read /api/config: %v", err)
	}
	payload := map[string]any{}
	if err := json.Unmarshal(body, &payload); err != nil {
		h.t.Skipf("/api/config is not JSON: %v", err)
	}
	webrtcBlock, ok := payload["webrtc"].(map[string]any)
	if !ok {
		h.t.Skipf("owncast at %s does not expose a webrtc config block", h.baseURL)
	}
	if enabled, _ := webrtcBlock["enabled"].(bool); !enabled {
		h.t.Skipf("webrtc delivery is disabled on %s; enable it for this stage", h.baseURL)
	}
}

func (h *harness) variant() string {
	if value := strings.TrimSpace(os.Getenv("OWNCAST_WHEP_VARIANT")); value != "" {
		return value
	}

	return "0"
}

func (h *harness) whepURL() string {
	return h.baseURL + whepPath + "?variant=" + h.variant()
}

func (h *harness) postOffer(offer string, mutate func(*http.Request)) *http.Response {
	h.t.Helper()

	request, err := http.NewRequest(http.MethodPost, h.whepURL(), strings.NewReader(offer))
	if err != nil {
		h.t.Fatalf("build WHEP request: %v", err)
	}
	request.Header.Set("Content-Type", "application/sdp")
	if mutate != nil {
		mutate(request)
	}
	response, err := h.client.Do(request)
	if err != nil {
		h.t.Fatalf("POST %s: %v", h.whepURL(), err)
	}

	return response
}

func readBody(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}

	return string(body)
}

func decodeError(t *testing.T, response *http.Response) map[string]string {
	t.Helper()

	body := readBody(t, response)
	payload := map[string]string{}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("error body is not JSON {code,message}: %q: %v", body, err)
	}
	if payload["code"] == "" {
		t.Fatalf("error body has no code: %q", body)
	}

	return payload
}

func (h *harness) deleteSession(t *testing.T, location string) {
	t.Helper()

	request, err := http.NewRequest(http.MethodDelete, h.baseURL+location, nil)
	if err != nil {
		t.Fatalf("build DELETE: %v", err)
	}
	response, err := h.client.Do(request)
	if err != nil {
		t.Fatalf("DELETE %s: %v", location, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE %s: status %d, want 204", location, response.StatusCode)
	}
}

// browserOffer builds a Pion viewer offer shaped like a browser offer: two
// recvonly transceivers and complete (non-trickle) candidates.
func browserOffer(t *testing.T) (*webrtc.PeerConnection, string) {
	t.Helper()

	settingEngine := webrtc.SettingEngine{}
	// A Pion receiver otherwise drops NACK retransmissions (same RTP sequence
	// number means the same SRTP index, which the replay window rejects).
	settingEngine.DisableSRTPReplayProtection(true)

	mediaEngine := &webrtc.MediaEngine{}
	if err := mediaEngine.RegisterDefaultCodecs(); err != nil {
		t.Fatalf("register codecs: %v", err)
	}
	registry := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(mediaEngine, registry); err != nil {
		t.Fatalf("register interceptors: %v", err)
	}
	api := webrtc.NewAPI(
		webrtc.WithSettingEngine(settingEngine),
		webrtc.WithMediaEngine(mediaEngine),
		webrtc.WithInterceptorRegistry(registry),
	)

	peerConnection, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create viewer PeerConnection: %v", err)
	}
	for _, kind := range []webrtc.RTPCodecType{webrtc.RTPCodecTypeVideo, webrtc.RTPCodecTypeAudio} {
		if _, err := peerConnection.AddTransceiverFromKind(kind, webrtc.RTPTransceiverInit{
			Direction: webrtc.RTPTransceiverDirectionRecvonly,
		}); err != nil {
			t.Fatalf("add %s transceiver: %v", kind, err)
		}
	}

	offer, err := peerConnection.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}
	gatherComplete := webrtc.GatheringCompletePromise(peerConnection)
	if err := peerConnection.SetLocalDescription(offer); err != nil {
		t.Fatalf("set local description: %v", err)
	}
	select {
	case <-gatherComplete:
	case <-time.After(connectTimeout):
		t.Fatalf("viewer ICE gathering did not complete")
	}

	return peerConnection, peerConnection.LocalDescription().SDP
}

func waitConnected(t *testing.T, peerConnection *webrtc.PeerConnection) {
	t.Helper()

	deadline := time.Now().Add(connectTimeout)
	for time.Now().Before(deadline) {
		state := peerConnection.ConnectionState()
		switch state {
		case webrtc.PeerConnectionStateConnected:
			return
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed:
			t.Fatalf("viewer PeerConnection %s", state)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("viewer PeerConnection did not connect within %s", connectTimeout)
}

type receivedMedia struct {
	mu          sync.Mutex
	video       []rtp.Packet
	audio       []rtp.Packet
	videoCodec  string
	audioCodec  string
	rtcpDrained bool
}

func (m *receivedMedia) attach(t *testing.T, peerConnection *webrtc.PeerConnection) {
	t.Helper()

	peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		m.mu.Lock()
		if track.Kind() == webrtc.RTPCodecTypeVideo {
			m.videoCodec = track.Codec().MimeType
		} else {
			m.audioCodec = track.Codec().MimeType
		}
		m.mu.Unlock()

		// Drain RTCP so the receiver report / NACK pipeline runs, like a browser.
		go func() {
			for {
				if _, _, err := receiver.ReadRTCP(); err != nil {
					return
				}
			}
		}()
		go func() {
			for {
				packet, _, err := track.ReadRTP()
				if err != nil {
					return
				}
				copied := *packet
				copied.Payload = append([]byte(nil), packet.Payload...)
				m.mu.Lock()
				if track.Kind() == webrtc.RTPCodecTypeVideo {
					m.video = append(m.video, copied)
				} else {
					m.audio = append(m.audio, copied)
				}
				m.mu.Unlock()
			}
		}()
	})
}

func (m *receivedMedia) snapshot() (video, audio []rtp.Packet, videoCodec, audioCodec string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	video = append([]rtp.Packet(nil), m.video...)
	audio = append([]rtp.Packet(nil), m.audio...)

	return video, audio, m.videoCodec, m.audioCodec
}

func (m *receivedMedia) firstVideoSSRC() uint32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.video) == 0 {
		return 0
	}

	return m.video[0].Header.SSRC
}

func assertLiveInputProtocol(t *testing.T, h *harness) {
	t.Helper()

	expected := strings.TrimSpace(os.Getenv("OWNCAST_EXPECT_INPUT_PROTOCOL"))
	if expected == "" {
		return
	}
	status, payload := h.getJSON("/api/status")
	if status != http.StatusOK {
		t.Fatalf("/api/status returned %d", status)
	}
	online, _ := payload["online"].(bool)
	if !online {
		t.Fatalf("expected a live stream for T3/T4, /api/status reports online=false")
	}
	protocol, _ := payload["inputProtocol"].(string)
	if !strings.EqualFold(protocol, expected) {
		t.Fatalf("inputProtocol = %q, want %q", protocol, expected)
	}
}

// TestWHEPContract covers the WHEP HTTP contract that does not need media.
func TestWHEPContract(t *testing.T) {
	h := newHarness(t)

	t.Run("OPTIONS preflight", func(t *testing.T) {
		request, err := http.NewRequest(http.MethodOptions, h.baseURL+whepPath, nil)
		if err != nil {
			t.Fatalf("build OPTIONS: %v", err)
		}
		request.Header.Set("Origin", h.baseURL)
		request.Header.Set("Access-Control-Request-Method", "POST")
		response, err := h.client.Do(request)
		if err != nil {
			t.Fatalf("OPTIONS: %v", err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusNoContent {
			t.Fatalf("OPTIONS status %d, want 204", response.StatusCode)
		}
		if response.Header.Get("Accept-Post") != "application/sdp" {
			t.Fatalf("OPTIONS Accept-Post = %q", response.Header.Get("Accept-Post"))
		}
	})

	t.Run("GET is not allowed", func(t *testing.T) {
		response, err := h.client.Get(h.whepURL())
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("GET status %d, want 405", response.StatusCode)
		}
	})

	t.Run("wrong content type is 415", func(t *testing.T) {
		request, err := http.NewRequest(http.MethodPost, h.whepURL(), strings.NewReader("{}"))
		if err != nil {
			t.Fatalf("build POST: %v", err)
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := h.client.Do(request)
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusUnsupportedMediaType {
			t.Fatalf("status %d, want 415", response.StatusCode)
		}
	})

	t.Run("malformed offer is 400 webrtc_bad_offer", func(t *testing.T) {
		response := h.postOffer("this is not sdp", nil)
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("status %d, want 400", response.StatusCode)
		}
		payload := decodeError(t, response)
		if payload["code"] != "webrtc_bad_offer" {
			t.Fatalf("code %q, want webrtc_bad_offer", payload["code"])
		}
	})

	t.Run("unsupported media is 422", func(t *testing.T) {
		offer := "v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\na=group:BUNDLE 0\r\nm=video 9 UDP/TLS/RTP/SAVPF 96\r\nc=IN IP4 0.0.0.0\r\na=mid:0\r\na=rtpmap:96 VP8/90000\r\n"
		response := h.postOffer(offer, nil)
		if response.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("status %d, want 422 (body %s)", response.StatusCode, readBody(t, response))
		}
		payload := decodeError(t, response)
		if payload["code"] != "webrtc_unsupported_media" && payload["code"] != "webrtc_codec_mismatch" {
			t.Fatalf("code %q, want webrtc_unsupported_media", payload["code"])
		}
	})

	t.Run("unknown variant is rejected", func(t *testing.T) {
		_, offer := browserOffer(t)
		request, err := http.NewRequest(http.MethodPost, h.whepURL(), strings.NewReader(offer))
		if err != nil {
			t.Fatalf("build POST: %v", err)
		}
		request.Header.Set("Content-Type", "application/sdp")
		request.URL.RawQuery = "variant=9999"
		response, err := h.client.Do(request)
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		if response.StatusCode != http.StatusBadRequest && response.StatusCode != http.StatusNotFound {
			t.Fatalf("status %d, want 400/404 (body %s)", response.StatusCode, readBody(t, response))
		}
	})

	t.Run("offer answer session lifecycle", func(t *testing.T) {
		peerConnection, offer := browserOffer(t)
		defer peerConnection.Close()

		response := h.postOffer(offer, nil)
		body := readBody(t, response)
		if response.StatusCode != http.StatusCreated {
			t.Fatalf("POST status %d, want 201 (body %s)", response.StatusCode, body)
		}
		location := response.Header.Get("Location")
		if !strings.HasPrefix(location, sessionPath) {
			t.Fatalf("Location %q does not start with %q", location, sessionPath)
		}
		if response.Header.Get("ETag") == "" {
			t.Fatalf("201 response has no ETag")
		}
		if !strings.Contains(body, "m=video") || !strings.Contains(body, "m=audio") {
			t.Fatalf("answer does not contain video/audio m-lines: %s", body)
		}
		if os.Getenv("OWNCAST_WHEP_EXPECT_LINK") == "1" {
			links := response.Header.Values("Link")
			if len(links) == 0 {
				t.Fatalf("expected Link rel=ice-server headers, got none")
			}
			for _, link := range links {
				if !strings.Contains(link, "rel=\"ice-server\"") {
					t.Fatalf("Link header %q is not rel=ice-server", link)
				}
			}
		}

		// Trickle a candidate the viewer already put in its offer: PATCH must
		// accept the fragment and answer 204.
		if candidate := firstCandidateLine(offer); candidate != "" {
			fragment := "m=video 9 UDP/TLS/RTP/SAVPF 96\r\na=mid:0\r\n" + candidate + "\r\n"
			patchRequest, err := http.NewRequest(http.MethodPatch, h.baseURL+location, strings.NewReader(fragment))
			if err != nil {
				t.Fatalf("build PATCH: %v", err)
			}
			patchRequest.Header.Set("Content-Type", "application/trickle-ice-sdpfrag")
			patchResponse, err := h.client.Do(patchRequest)
			if err != nil {
				t.Fatalf("PATCH: %v", err)
			}
			patchResponse.Body.Close()
			if patchResponse.StatusCode != http.StatusNoContent {
				t.Fatalf("PATCH status %d, want 204", patchResponse.StatusCode)
			}
		}

		h.deleteSession(t, location)
		// DELETE is idempotent.
		deleteRequest, err := http.NewRequest(http.MethodDelete, h.baseURL+location, nil)
		if err != nil {
			t.Fatalf("build second DELETE: %v", err)
		}
		secondDelete, err := h.client.Do(deleteRequest)
		if err != nil {
			t.Fatalf("second DELETE: %v", err)
		}
		secondDelete.Body.Close()
		if secondDelete.StatusCode != http.StatusNoContent {
			t.Fatalf("second DELETE status %d, want 204", secondDelete.StatusCode)
		}
	})
}

func firstCandidateLine(sdp string) string {
	for _, line := range strings.Split(sdp, "\n") {
		line = strings.TrimSuffix(strings.TrimSpace(line), "")
		if strings.HasPrefix(line, "a=candidate:") {
			return line
		}
	}

	return ""
}

// TestWHEPMediaDelivery is T3/T4: RTMP or SRT published, non-trickle WHEP
// offer, ICE connection, inbound H.264 video and Opus audio RTP, RTCP feedback
// acceptance, then DELETE teardown.
func TestWHEPMediaDelivery(t *testing.T) {
	if os.Getenv("OWNCAST_WHEP_EXPECT_LIVE") != "1" {
		t.Skip("set OWNCAST_WHEP_EXPECT_LIVE=1 with a live RTMP/SRT publisher for T3/T4")
	}
	h := newHarness(t)
	assertLiveInputProtocol(t, h)

	peerConnection, offer := browserOffer(t)
	defer peerConnection.Close()

	media := &receivedMedia{}
	media.attach(t, peerConnection)

	response := h.postOffer(offer, nil)
	body := readBody(t, response)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("POST status %d, want 201 (body %s)", response.StatusCode, body)
	}
	location := response.Header.Get("Location")

	if err := peerConnection.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  body,
	}); err != nil {
		t.Fatalf("set remote answer: %v", err)
	}
	waitConnected(t, peerConnection)

	deadline := time.Now().Add(mediaTimeout)
	for time.Now().Before(deadline) {
		video, audio, _, _ := media.snapshot()
		if len(video) > 0 && len(audio) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	video, audio, videoCodec, audioCodec := media.snapshot()
	if len(video) == 0 {
		t.Fatalf("no video RTP received within %s", mediaTimeout)
	}
	if len(audio) == 0 {
		t.Fatalf("no audio RTP received within %s", mediaTimeout)
	}
	if !strings.EqualFold(videoCodec, webrtc.MimeTypeH264) {
		t.Fatalf("video codec %q, want %s", videoCodec, webrtc.MimeTypeH264)
	}
	if !strings.EqualFold(audioCodec, webrtc.MimeTypeOpus) {
		t.Fatalf("audio codec %q, want %s", audioCodec, webrtc.MimeTypeOpus)
	}

	// Exercise the RTCP feedback path from the viewer: a PLI must be accepted
	// by the server. The server cannot force an FFmpeg keyframe, so recovery is
	// not asserted here; the per-viewer counters are covered by the
	// services/webrtc integration tests (probe checks A5/A6).
	videoSSRC := media.firstVideoSSRC()
	if videoSSRC == 0 {
		videoSSRC = video[0].Header.SSRC
	}
	if err := peerConnection.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: videoSSRC}}); err != nil {
		t.Fatalf("send PLI: %v", err)
	}

	// Teardown must close the media path.
	h.deleteSession(t, location)
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		state := peerConnection.ConnectionState()
		if state == webrtc.PeerConnectionStateClosed ||
			state == webrtc.PeerConnectionStateFailed ||
			state == webrtc.PeerConnectionStateDisconnected {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("viewer PeerConnection stayed %s after DELETE", peerConnection.ConnectionState())
}

// TestWHEPCapacityRejection covers 503 + Retry-After with maxViewers=1.
func TestWHEPCapacityRejection(t *testing.T) {
	if os.Getenv("OWNCAST_WHEP_CAPACITY_TEST") != "1" {
		t.Skip("set OWNCAST_WHEP_CAPACITY_TEST=1 when the server runs with maxViewers=1")
	}
	h := newHarness(t)

	firstPeer, firstOffer := browserOffer(t)
	defer firstPeer.Close()
	first := h.postOffer(firstOffer, nil)
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first viewer status %d, want 201 (body %s)", first.StatusCode, readBody(t, first))
	}
	location := first.Header.Get("Location")
	readBody(t, first)

	secondPeer, secondOffer := browserOffer(t)
	defer secondPeer.Close()
	second := h.postOffer(secondOffer, nil)
	if second.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("second viewer status %d, want 503", second.StatusCode)
	}
	if second.Header.Get("Retry-After") == "" {
		t.Fatalf("503 response has no Retry-After")
	}
	payload := decodeError(t, second)
	if payload["code"] != "webrtc_capacity" && payload["code"] != "webrtc_per_ip_capacity" {
		t.Fatalf("capacity code %q", payload["code"])
	}

	h.deleteSession(t, location)
}

// TestWHEPRateLimit covers 429 + Retry-After with signalingRateLimit=1.
func TestWHEPRateLimit(t *testing.T) {
	if os.Getenv("OWNCAST_WHEP_RATELIMIT_TEST") != "1" {
		t.Skip("set OWNCAST_WHEP_RATELIMIT_TEST=1 when the server runs with signalingRateLimit=1")
	}
	h := newHarness(t)

	_, offer := browserOffer(t)
	first := h.postOffer(offer, nil)
	firstBody := readBody(t, first)
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first POST status %d, want 201 (body %s)", first.StatusCode, firstBody)
	}
	if location := first.Header.Get("Location"); location != "" {
		h.deleteSession(t, location)
	}

	second := h.postOffer(offer, nil)
	if second.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second POST status %d, want 429 (body %s)", second.StatusCode, readBody(t, second))
	}
	if second.Header.Get("Retry-After") == "" {
		t.Fatalf("429 response has no Retry-After")
	}
	payload := decodeError(t, second)
	if payload["code"] != "webrtc_rate_limited" {
		t.Fatalf("code %q, want webrtc_rate_limited", payload["code"])
	}
}

// TestWHEPStrictDomain covers the strictDomain Host check.
func TestWHEPStrictDomain(t *testing.T) {
	if os.Getenv("OWNCAST_WHEP_STRICT_DOMAIN_TEST") != "1" {
		t.Skip("set OWNCAST_WHEP_STRICT_DOMAIN_TEST=1 when the server runs with strictDomain=true")
	}
	domain := strings.TrimSpace(os.Getenv("OWNCAST_WHEP_DOMAIN"))
	if domain == "" {
		t.Fatalf("OWNCAST_WHEP_STRICT_DOMAIN_TEST=1 requires OWNCAST_WHEP_DOMAIN")
	}
	h := newHarness(t)

	peerConnection, offer := browserOffer(t)
	defer peerConnection.Close()

	response := h.postOffer(offer, func(request *http.Request) {
		request.Host = "not-" + domain
	})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("mismatched Host status %d, want 403 (body %s)", response.StatusCode, readBody(t, response))
	}
	payload := decodeError(t, response)
	if payload["code"] != "webrtc_domain_mismatch" {
		t.Fatalf("code %q, want webrtc_domain_mismatch", payload["code"])
	}
}
