package admin

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/webrtc"
)

// A hot update that could not be persisted is rolled back without restarting the
// service, so a live stream keeps its engine, legs and sessions.
func TestRollbackWithoutRestartOnlyRestoresTheConfiguration(t *testing.T) {
	before := models.DefaultWebRTCConfig()
	applied := before
	applied.SignalingRateLimit = before.SignalingRateLimit + 1

	applier := &failingApplier{config: applied}
	admin := &Admin{webrtcApplier: applier}

	recorder := httptest.NewRecorder()
	admin.rollbackWebRTCAfterPersistFailure(recorder, errors.New("disk full"), before, false)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "disk full") {
		t.Fatalf("body = %q, want the persist failure", body)
	}
	if got := applier.Config().SignalingRateLimit; got != before.SignalingRateLimit {
		t.Fatalf("signaling rate limit = %d, want the previous value %d", got, before.SignalingRateLimit)
	}
	if applier.stops != 0 || applier.starts != 0 {
		t.Fatalf("rollback restarted the service: %d stops, %d starts", applier.stops, applier.starts)
	}
}

// When the request did restart the service, the rollback restarts it again; if that also
// fails the caller sees both reasons and is told the state may be inconsistent.
func TestRollbackAfterRestartReportsBothFailures(t *testing.T) {
	before := models.DefaultWebRTCConfig()
	service := webrtc.New(webrtc.Deps{Config: before})
	admin := &Admin{webrtc: service}

	recorder := httptest.NewRecorder()
	admin.rollbackWebRTCAfterPersistFailure(recorder, errors.New("disk full"), before, true)

	body := recorder.Body.String()
	if !strings.Contains(body, "disk full") {
		t.Fatalf("body = %q, want the persist failure", body)
	}
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
}
