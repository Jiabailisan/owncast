package admin

import (
	"encoding/json"
	"testing"
)

func TestWebRTCClientReportRequestJSONContract(t *testing.T) {
	payload := []byte(`{
		"sessionId":"0123456789abcdef0123456789abcdef",
		"decodedWidth":2560,
		"decodedHeight":1440,
		"fps":59.9,
		"packetsLost":126,
		"packetsReceived":8763,
		"packetLossPct":1.42,
		"jitterMs":0.8,
		"roundTripTimeMs":3,
		"transport":"udp",
		"videoBitrateKbps":5980
	}`)

	request := webrtcClientReportRequest{}
	if err := json.Unmarshal(payload, &request); err != nil {
		t.Fatalf("unmarshal beacon payload: %v", err)
	}
	if request.SessionID != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("sessionId = %q", request.SessionID)
	}
	if request.DecodedWidth != 2560 || request.DecodedHeight != 1440 {
		t.Fatalf("decoded size = %dx%d", request.DecodedWidth, request.DecodedHeight)
	}
	if request.FPS != 59.9 {
		t.Fatalf("fps = %v", request.FPS)
	}
	if request.PacketsLost != 126 || request.PacketsReceived != 8763 {
		t.Fatalf("packets = lost %d received %d", request.PacketsLost, request.PacketsReceived)
	}
	if request.PacketLossPct == nil || *request.PacketLossPct != 1.42 {
		t.Fatalf("packetLossPct = %v", request.PacketLossPct)
	}
	if request.Transport != "udp" {
		t.Fatalf("transport = %q", request.Transport)
	}
	if request.VideoBitrateKbps == nil || *request.VideoBitrateKbps != 5980 {
		t.Fatalf("videoBitrateKbps = %v", request.VideoBitrateKbps)
	}
}
