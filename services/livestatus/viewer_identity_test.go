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

// Two sessions behind one public address must each keep their own name, quality and
// metrics, whichever order the reports arrive in.
func TestWebRTCReportsAreAttributedBySession(t *testing.T) {
	qualityA, latencyA := "720p60", 980.0
	qualityB, latencyB := "1080p60", 1120.0

	rows := []models.LiveViewer{
		{ClientID: "session-a", IPAddress: "198.51.100.7", Protocol: "webrtc"},
		{ClientID: "session-b", IPAddress: "198.51.100.7", Protocol: "webrtc"},
	}
	reports := map[string]models.Viewer{
		"session-a": {Protocol: "webrtc", DisplayName: "alice", ReportedQuality: qualityA, ReportedLatency: &latencyA},
		"session-b": {Protocol: "webrtc", DisplayName: "bob", ReportedQuality: qualityB, ReportedLatency: &latencyB},
	}

	applyWebRTCReports(rows, reports)

	assertSession(t, rows[0], "alice", qualityA, latencyA)
	assertSession(t, rows[1], "bob", qualityB, latencyB)

	// The same reports in the opposite order must land on the same sessions.
	reversed := []models.LiveViewer{
		{ClientID: "session-b", IPAddress: "198.51.100.7", Protocol: "webrtc"},
		{ClientID: "session-a", IPAddress: "198.51.100.7", Protocol: "webrtc"},
	}
	applyWebRTCReports(reversed, reports)

	assertSession(t, reversed[0], "bob", qualityB, latencyB)
	assertSession(t, reversed[1], "alice", qualityA, latencyA)
}

func assertSession(t *testing.T, row models.LiveViewer, name, quality string, latency float64) {
	t.Helper()

	if row.Username != name {
		t.Fatalf("session %s name = %q, want %q", row.ClientID, row.Username, name)
	}
	if row.Quality != quality {
		t.Fatalf("session %s quality = %q, want %q", row.ClientID, row.Quality, quality)
	}
	if row.LatencyMs == nil {
		t.Fatalf("session %s has no latency", row.ClientID)
	}
	if *row.LatencyMs != latency {
		t.Fatalf("session %s latency = %v, want %v", row.ClientID, *row.LatencyMs, latency)
	}
}

// A session that reported nothing keeps what the server measured for it.
func TestWebRTCReportWithoutASessionKeepsServerData(t *testing.T) {
	serverBitrate := 2400.0
	quality := "720p60"

	rows := []models.LiveViewer{
		{ClientID: "session-a", IPAddress: "198.51.100.7", Protocol: "webrtc", BitrateKbps: &serverBitrate},
		{ClientID: "session-b", IPAddress: "198.51.100.7", Protocol: "webrtc"},
	}
	reports := map[string]models.Viewer{
		"session-b": {Protocol: "webrtc", DisplayName: "bob", ReportedQuality: quality},
	}

	applyWebRTCReports(rows, reports)

	if rows[0].Username != "" || rows[0].Quality != "" || rows[0].LatencyMs != nil {
		t.Fatalf("the silent session absorbed a report: %+v", rows[0])
	}
	if rows[0].BitrateKbps == nil || *rows[0].BitrateKbps != serverBitrate {
		t.Fatalf("the silent session lost its server data: %+v", rows[0].BitrateKbps)
	}
	if rows[1].Username != "bob" || rows[1].Quality != quality {
		t.Fatalf("the reporting session = %+v, want its own report", rows[1])
	}
}

// An HLS row on the same address never absorbs a WebRTC report, and a WebRTC row never
// takes a report that belongs to another session.
func TestWebRTCReportsIgnoreHLSRows(t *testing.T) {
	quality := "720p60"
	rows := []models.LiveViewer{
		{ClientID: "hls", IPAddress: "198.51.100.7", Protocol: "hls"},
		{ClientID: "session-a", IPAddress: "198.51.100.7", Protocol: "webrtc"},
	}
	reports := map[string]models.Viewer{
		"session-a": {Protocol: "webrtc", ReportedQuality: quality},
	}

	applyWebRTCReports(rows, reports)

	if rows[0].Quality != "" {
		t.Fatalf("the HLS row absorbed a WebRTC report: %q", rows[0].Quality)
	}
	if rows[1].Quality != quality {
		t.Fatalf("the WebRTC row quality = %q, want %q", rows[1].Quality, quality)
	}
}

// The chat name only overrides a WebRTC row when the token belongs to that session.
func TestDisplayNamesUseTheSessionToken(t *testing.T) {
	service := &Service{names: fakeNames{"token-a": "alice", "token-b": "bob"}}

	rows := []models.LiveViewer{
		{ClientID: "session-a", Protocol: "webrtc"},
		{ClientID: "session-b", Protocol: "webrtc"},
	}
	tokens := map[string]string{
		"webrtc:session-a": "token-a",
		"webrtc:session-b": "token-b",
	}

	service.applyDisplayNames(rows, tokens)

	if rows[0].Username != "alice" || rows[1].Username != "bob" {
		t.Fatalf("names = %q and %q, want each session's own name", rows[0].Username, rows[1].Username)
	}
}
