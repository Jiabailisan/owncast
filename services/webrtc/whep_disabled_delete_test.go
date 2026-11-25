package webrtc

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWHEPAllowsSessionDeleteWhenDisabled(t *testing.T) {
	svc := testService(t, nil)
	setDeliverableVariants(t, svc, testWebRTCVariants(1))

	_, offer := testBrowserOffer(t)
	created := whepRequest(t, svc, http.MethodPost, "/webrtc/whep", "application/sdp", offer, nil)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	sessionLoc := created.Header().Get("Location")
	require.NotEmpty(t, sessionLoc)
	require.Equal(t, 1, svc.ViewerCount())

	// Disable WebRTC delivery: new POST offers must be rejected with 503,
	// while DELETE on the existing session must still succeed with 204.
	svc.mu.Lock()
	svc.cfg.Enabled = false
	svc.mu.Unlock()

	rejected := whepRequest(t, svc, http.MethodPost, "/webrtc/whep", "application/sdp", offer, nil)
	assert.Equal(t, http.StatusServiceUnavailable, rejected.Code)

	deleted := whepRequest(t, svc, http.MethodDelete, sessionLoc, "", "", nil)
	assert.Equal(t, http.StatusNoContent, deleted.Code)
	assert.Equal(t, 0, svc.ViewerCount())
}
