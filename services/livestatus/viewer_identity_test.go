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

// A WebRTC row's chat name comes from the token its own session reported, and the token
// never appears in the row.
func TestWebRTCNamesComeFromTheSessionToken(t *testing.T) {
	service := &Service{names: fakeNames{"token-a": "alice", "token-b": "bob"}}

	rows := []models.LiveViewer{
		{ClientID: "session-a", IPAddress: "198.51.100.7", Protocol: "webrtc", NameToken: "token-a"},
		{ClientID: "session-b", IPAddress: "198.51.100.7", Protocol: "webrtc", NameToken: "token-b"},
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

// A session that reported no token keeps the name the server already had, and one session's
// token never reaches the other row.
func TestWebRTCNamesDoNotLeakBetweenSessions(t *testing.T) {
	service := &Service{names: fakeNames{"token-b": "bob"}}

	rows := []models.LiveViewer{
		{ClientID: "session-a", Protocol: "webrtc", Username: "server-name"},
		{ClientID: "session-b", Protocol: "webrtc", NameToken: "token-b"},
	}

	service.applyDisplayNames(rows, map[string]string{"webrtc:session-b": "token-b"})

	if rows[0].Username != "server-name" {
		t.Fatalf("the silent session's name = %q, want the server value", rows[0].Username)
	}
	if rows[1].Username != "bob" {
		t.Fatalf("the reporting session's name = %q, want bob", rows[1].Username)
	}
}

// An HLS row keeps its own client identity and never takes a WebRTC token.
func TestHLSRowsKeepTheirOwnIdentity(t *testing.T) {
	service := &Service{names: fakeNames{"token-hls": "carol"}}

	rows := []models.LiveViewer{
		{ClientID: "client-hls", Protocol: "hls"},
	}
	tokens := map[string]string{"client:client-hls": "token-hls"}

	service.applyDisplayNames(rows, tokens)

	if rows[0].Username != "carol" {
		t.Fatalf("HLS name = %q, want carol", rows[0].Username)
	}
}
