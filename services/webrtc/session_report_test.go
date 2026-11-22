package webrtc

import (
	"net/http"
	"strings"
	"testing"

	"github.com/owncast/owncast/models"
	"github.com/stretchr/testify/require"
)

// Two sessions behind one public address must each carry the report that names their own
// session: the viewer list reads the session, never the address.
func TestLiveViewersCarryTheirOwnSessionReport(t *testing.T) {
	svc := testService(t, nil)
	setDeliverableVariants(t, svc, testWebRTCVariants(1))

	_, offer := testBrowserOffer(t)
	create := func() string {
		created := whepRequest(t, svc, http.MethodPost, "/webrtc/whep", "application/sdp", offer, func(r *http.Request) {
			r.RemoteAddr = "198.51.100.7:40000"
		})
		require.Equal(t, http.StatusCreated, created.Code)

		return strings.TrimPrefix(created.Header().Get("Location"), "/webrtc/whep/session/")
	}

	idA, idB := create(), create()
	require.NotEqual(t, idA, idB)

	latencyA, latencyB := 980.0, 1120.0
	require.True(t, svc.ApplyClientReport(idA, ClientReport{LatencyMs: &latencyA, NameToken: "token-a"}))
	require.True(t, svc.ApplyClientReport(idB, ClientReport{LatencyMs: &latencyB, NameToken: "token-b"}))

	rows := map[string]models.LiveViewer{}
	for _, row := range svc.LiveViewers() {
		rows[row.ClientID] = row
	}

	rowA, ok := rows[idA]
	require.True(t, ok, "session A missing from the viewer list")
	require.NotNil(t, rowA.LatencyMs)
	require.Equal(t, latencyA, *rowA.LatencyMs)
	require.Equal(t, "token-a", rowA.NameToken)

	rowB, ok := rows[idB]
	require.True(t, ok, "session B missing from the viewer list")
	require.NotNil(t, rowB.LatencyMs)
	require.Equal(t, latencyB, *rowB.LatencyMs)
	require.Equal(t, "token-b", rowB.NameToken)

	// The quality is the rendition the server negotiated, not something the player said.
	require.NotEmpty(t, rowA.Quality)
	require.Equal(t, rowA.Quality, rowB.Quality)
}

// A session that reported nothing keeps the data the server measured.
func TestLiveViewerWithoutAReportKeepsServerData(t *testing.T) {
	svc := testService(t, nil)
	setDeliverableVariants(t, svc, testWebRTCVariants(1))

	_, offer := testBrowserOffer(t)
	created := whepRequest(t, svc, http.MethodPost, "/webrtc/whep", "application/sdp", offer, func(r *http.Request) {
		r.RemoteAddr = "198.51.100.9:40000"
	})
	require.Equal(t, http.StatusCreated, created.Code)
	id := strings.TrimPrefix(created.Header().Get("Location"), "/webrtc/whep/session/")

	rows := svc.LiveViewers()
	require.Len(t, rows, 1)
	require.Equal(t, id, rows[0].ClientID)
	require.Nil(t, rows[0].LatencyMs)
	require.Empty(t, rows[0].NameToken)
	require.NotEmpty(t, rows[0].Quality)
}
