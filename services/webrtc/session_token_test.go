package webrtc

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// createSessionForReport opens a real WHEP session and returns its id.
func createSessionForReport(t *testing.T, svc *Service, offer string, remoteAddr string) string {
	t.Helper()

	created := whepRequest(t, svc, http.MethodPost, "/webrtc/whep", "application/sdp", offer, func(r *http.Request) {
		r.RemoteAddr = remoteAddr
	})
	require.Equal(t, http.StatusCreated, created.Code)

	return strings.TrimPrefix(created.Header().Get("Location"), "/webrtc/whep/session/")
}

// A report without a token clears the one the session reported before, so a logged-out
// viewer stops showing the name of the identity it used to have.
func TestReportWithoutTokenClearsThePreviousToken(t *testing.T) {
	svc := testService(t, nil)
	setDeliverableVariants(t, svc, testWebRTCVariants(1))

	_, offer := testBrowserOffer(t)
	id := createSessionForReport(t, svc, offer, "198.51.100.20:40000")

	require.True(t, svc.ApplyClientReport(id, ClientReport{NameToken: "token-a"}))

	rows := svc.LiveViewers()
	require.Len(t, rows, 1)
	require.Equal(t, "token-a", rows[0].NameToken)

	// The next report carries no token: the stored one must go with it.
	require.True(t, svc.ApplyClientReport(id, ClientReport{}))

	rows = svc.LiveViewers()
	require.Len(t, rows, 1)
	require.Empty(t, rows[0].NameToken)
}

// The token is server-side state: it never appears in the serialized viewer row.
func TestViewerRowSerializationOmitsTheToken(t *testing.T) {
	svc := testService(t, nil)
	setDeliverableVariants(t, svc, testWebRTCVariants(1))

	_, offer := testBrowserOffer(t)
	id := createSessionForReport(t, svc, offer, "198.51.100.21:40000")
	require.True(t, svc.ApplyClientReport(id, ClientReport{NameToken: "token-secret"}))

	rows := svc.LiveViewers()
	require.Len(t, rows, 1)

	encoded, err := json.Marshal(rows[0])
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "token-secret")
	require.NotContains(t, string(encoded), "nameToken")
}
