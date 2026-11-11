package livestatus

import (
	"testing"

	"github.com/owncast/owncast/models"
)

// fakeNames is a NameProvider backed by a fixed token table.
type fakeNames map[string]string

func (f fakeNames) DisplayNameForToken(token string) string { return f[token] }

func (f fakeNames) DisplayNamesForTokens(tokens []string) map[string]string {
	resolved := map[string]string{}
	for _, token := range tokens {
		if name, ok := f[token]; ok {
			resolved[token] = name
		}
	}

	return resolved
}

// TestDistributeWebRTCReportsSplitsOneAddress is the regression test for two
// sessions behind one NAT: matching by address and stopping at the first row gave
// every report to the first viewport and left the second empty.
func TestDistributeWebRTCReportsSplitsOneAddress(t *testing.T) {
	qualityA := "720p60"
	latencyA := 980.0
	qualityB := "1080p60"
	latencyB := 1120.0

	rows := []models.LiveViewer{
		{ClientID: "session-a", IPAddress: "198.51.100.7", Protocol: "webrtc"},
		{ClientID: "session-b", IPAddress: "198.51.100.7", Protocol: "webrtc"},
	}
	reports := map[string][]models.Viewer{
		"198.51.100.7": {
			{Protocol: "webrtc", ReportedQuality: qualityA, ReportedLatency: &latencyA},
			{Protocol: "webrtc", ReportedQuality: qualityB, ReportedLatency: &latencyB},
		},
	}

	distributeWebRTCReports(rows, reports)

	if rows[0].Quality != qualityA || rows[1].Quality != qualityB {
		t.Fatalf("each row must get its own report, got %q and %q", rows[0].Quality, rows[1].Quality)
	}
	if rows[0].LatencyMs == nil || rows[1].LatencyMs == nil {
		t.Fatal("both rows must carry a latency measurement")
	}
	if *rows[0].LatencyMs == *rows[1].LatencyMs {
		t.Fatal("the two rows share one report instead of consuming two")
	}
}

// TestDistributeWebRTCReportsIgnoresHLSRows proves an HLS viewer on the same
// address does not absorb a WebRTC player's report.
func TestDistributeWebRTCReportsIgnoresHLSRows(t *testing.T) {
	quality := "720p60"
	rows := []models.LiveViewer{
		{ClientID: "hls", IPAddress: "198.51.100.7", Protocol: "hls"},
		{ClientID: "webrtc", IPAddress: "198.51.100.7", Protocol: "webrtc"},
	}
	reports := map[string][]models.Viewer{
		"198.51.100.7": {{Protocol: "webrtc", ReportedQuality: quality}},
	}

	distributeWebRTCReports(rows, reports)

	if rows[0].Quality != "" {
		t.Fatalf("an HLS row must not take a WebRTC report, got %q", rows[0].Quality)
	}
	if rows[1].Quality != quality {
		t.Fatalf("the WebRTC row must take the report, got %q", rows[1].Quality)
	}
}

// TestApplyDisplayNamesKeepsHLSIdentity is the regression test for an HLS viewer
// inheriting the chat name of whoever watches over WebRTC from the same address.
func TestApplyDisplayNamesKeepsHLSIdentity(t *testing.T) {
	service := &Service{names: fakeNames{"webrtc-token": "WebRTC Person"}}

	rows := []models.LiveViewer{
		{ClientID: "webrtc-session", IPAddress: "198.51.100.7", Protocol: "webrtc"},
		{ClientID: "hls-client", IPAddress: "198.51.100.7", Protocol: "hls"},
	}
	tokens := map[string]string{
		"webrtc:198.51.100.7": "webrtc-token",
	}

	service.applyDisplayNames(rows, tokens)

	if rows[0].Username != "WebRTC Person" {
		t.Fatalf("the WebRTC row should use the address lookup, got %q", rows[0].Username)
	}
	if rows[1].Username != "" {
		t.Fatalf("the HLS row must not borrow a name, got %q", rows[1].Username)
	}
}

// TestApplyDisplayNamesUsesClientToken proves an HLS row still resolves its own name.
func TestApplyDisplayNamesUsesClientToken(t *testing.T) {
	service := &Service{names: fakeNames{"hls-token": "HLS Person"}}

	rows := []models.LiveViewer{{ClientID: "hls-client", IPAddress: "198.51.100.9", Protocol: "hls"}}
	tokens := map[string]string{"client:hls-client": "hls-token"}

	service.applyDisplayNames(rows, tokens)

	if rows[0].Username != "HLS Person" {
		t.Fatalf("expected the HLS viewer's own name, got %q", rows[0].Username)
	}
}
