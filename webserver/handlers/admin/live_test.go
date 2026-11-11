package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/owncast/owncast/models"
)

func TestNormalizeLiveViewersPagination(t *testing.T) {
	tests := []struct {
		name         string
		page         int
		pageSize     int
		wantPage     int
		wantPageSize int
	}{
		{name: "defaults", page: 0, pageSize: 0, wantPage: 1, wantPageSize: 25},
		{name: "negative", page: -3, pageSize: -1, wantPage: 1, wantPageSize: 25},
		{name: "clamp page size", page: 2, pageSize: 500, wantPage: 2, wantPageSize: 200},
		{name: "in range", page: 3, pageSize: 50, wantPage: 3, wantPageSize: 50},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			page, pageSize := normalizeLiveViewersPagination(test.page, test.pageSize)
			if page != test.wantPage || pageSize != test.wantPageSize {
				t.Fatalf("got (%d, %d), want (%d, %d)", page, pageSize, test.wantPage, test.wantPageSize)
			}
		})
	}
}

func TestNormalizeLiveViewersParameters(t *testing.T) {
	sortCases := map[string]string{
		"duration":    liveSortDuration,
		"connectedAt": liveSortConnectedAt,
		"protocol":    liveSortProtocol,
		"bitrate":     liveSortBitrate,
		"":            liveSortConnectedAt,
		"bogus":       liveSortConnectedAt,
	}
	for input, want := range sortCases {
		if got := normalizeLiveViewersSort(input); got != want {
			t.Errorf("normalizeLiveViewersSort(%q) = %q, want %q", input, got, want)
		}
	}

	orderCases := map[string]string{
		"asc":  liveOrderAscending,
		"desc": liveOrderDescending,
		"":     liveOrderDescending,
		"side": liveOrderDescending,
	}
	for input, want := range orderCases {
		if got := normalizeLiveViewersOrder(input); got != want {
			t.Errorf("normalizeLiveViewersOrder(%q) = %q, want %q", input, got, want)
		}
	}

	protocolCases := map[string]string{
		"hls":    liveProtocolFilterHLS,
		"webrtc": liveProtocolFilterWebRTC,
		"all":    liveProtocolFilterAll,
		"":       liveProtocolFilterAll,
		"bogus":  liveProtocolFilterAll,
	}
	for input, want := range protocolCases {
		if got := normalizeLiveViewersProtocol(input); got != want {
			t.Errorf("normalizeLiveViewersProtocol(%q) = %q, want %q", input, got, want)
		}
	}
}

func decodeBody(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", recorder.Code, recorder.Body.String())
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", contentType)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return payload
}

func TestGetLiveStatusWithoutAggregatorIsOffline(t *testing.T) {
	handler := New(Deps{})
	recorder := httptest.NewRecorder()
	handler.GetLiveStatus(recorder, httptest.NewRequest(http.MethodGet, "/api/admin/live/status", nil))

	payload := decodeBody(t, recorder)

	if payload["state"] != models.LiveStateOffline {
		t.Errorf("state = %v, want %q", payload["state"], models.LiveStateOffline)
	}
	if payload["inputProtocol"] != "" {
		t.Errorf("inputProtocol = %v, want empty", payload["inputProtocol"])
	}
	if warnings, ok := payload["warnings"].([]any); !ok || len(warnings) != 0 {
		t.Errorf("warnings = %v, want empty array", payload["warnings"])
	}

	ffmpeg, ok := payload["ffmpeg"].(map[string]any)
	if !ok {
		t.Fatalf("ffmpeg block missing: %v", payload["ffmpeg"])
	}
	if processes, ok := ffmpeg["processes"].([]any); !ok || len(processes) != 0 {
		t.Errorf("ffmpeg.processes = %v, want empty array", ffmpeg["processes"])
	}

	viewers, ok := payload["viewers"].(map[string]any)
	if !ok {
		t.Fatalf("viewers block missing: %v", payload["viewers"])
	}
	for _, key := range []string{"total", "webrtc", "hls"} {
		if _, ok := viewers[key].(float64); !ok {
			t.Errorf("viewers.%s missing or not numeric: %v", key, viewers[key])
		}
	}

	srt, ok := payload["srt"].(map[string]any)
	if !ok {
		t.Fatalf("srt block missing: %v", payload["srt"])
	}
	if _, ok := srt["enabled"].(bool); !ok {
		t.Errorf("srt.enabled missing: %v", srt["enabled"])
	}
	if _, ok := srt["state"].(string); !ok {
		t.Errorf("srt.state missing: %v", srt["state"])
	}
}

func TestGetLiveStatusIncludesSRTListenerState(t *testing.T) {
	handler := New(Deps{})
	recorder := httptest.NewRecorder()
	handler.GetLiveStatus(recorder, httptest.NewRequest(http.MethodGet, "/api/admin/live/status", nil))

	payload := decodeBody(t, recorder)
	srt, ok := payload["srt"].(map[string]any)
	if !ok {
		t.Fatalf("srt block missing: %v", payload["srt"])
	}
	// No SRT service injected: the block must still have the fixed offline shape.
	if srt["state"] != "stopped" {
		t.Errorf("srt.state = %v, want stopped", srt["state"])
	}
	if srt["listening"] != false {
		t.Errorf("srt.listening = %v, want false", srt["listening"])
	}
}

func TestGetLiveViewersWithoutAggregatorIsEmptyPage(t *testing.T) {
	handler := New(Deps{})
	recorder := httptest.NewRecorder()
	handler.GetLiveViewers(recorder, httptest.NewRequest(http.MethodGet, "/api/admin/live/viewers", nil), 0, 500, "bogus", "bogus", "bogus", "  ")

	payload := decodeBody(t, recorder)
	if payload["page"] != float64(1) {
		t.Errorf("page = %v, want 1", payload["page"])
	}
	if payload["pageSize"] != float64(200) {
		t.Errorf("pageSize = %v, want 200", payload["pageSize"])
	}
	if viewers, ok := payload["viewers"].([]any); !ok || len(viewers) != 0 {
		t.Errorf("viewers = %v, want empty array", payload["viewers"])
	}
}

func TestGetLiveFFmpegWithoutStreamIsEmpty(t *testing.T) {
	handler := New(Deps{})
	recorder := httptest.NewRecorder()
	handler.GetLiveFFmpeg(recorder, httptest.NewRequest(http.MethodGet, "/api/admin/live/ffmpeg", nil))

	payload := decodeBody(t, recorder)
	if _, ok := payload["processModel"].(string); !ok {
		t.Errorf("processModel missing: %v", payload["processModel"])
	}
	if processes, ok := payload["processes"].([]any); !ok || len(processes) != 0 {
		t.Errorf("processes = %v, want empty array", payload["processes"])
	}
}
