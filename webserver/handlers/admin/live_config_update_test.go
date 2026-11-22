package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/owncast/owncast/config"
	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/persistence/configrepository"
	"github.com/owncast/owncast/services/datastore"
)

// isolatedRepository builds a repository on its own database file.
func isolatedRepository(t *testing.T) configrepository.ConfigRepository {
	t.Helper()

	dbFile, err := os.CreateTemp(t.TempDir(), "owncast-config-test-*.db")
	if err != nil {
		t.Fatalf("temp db: %v", err)
	}
	dbFile.Close()

	ds, err := datastore.SetupPersistence(dbFile.Name(), t.TempDir())
	if err != nil {
		t.Fatalf("datastore: %v", err)
	}

	return configrepository.New(ds)
}

// failingApplier is a WebRTC service double whose restart can be made to fail.
type failingApplier struct {
	config      models.WebRTCConfig
	stopErrors  []error
	startErrors []error
	stops       int
	starts      int
	// applied records every configuration the update path applied, in order.
	applied []models.WebRTCConfig
}

func (f *failingApplier) Config() models.WebRTCConfig { return f.config }

func (f *failingApplier) UpdateConfig(config models.WebRTCConfig) ([]string, error) {
	f.config = config
	f.applied = append(f.applied, config)

	return []string{"webrtc.port"}, nil
}

func (f *failingApplier) PendingRestartFields() []string { return nil }

func (f *failingApplier) Stop(context.Context) error {
	f.stops++
	if len(f.stopErrors) >= f.stops {
		return f.stopErrors[f.stops-1]
	}

	return nil
}

func (f *failingApplier) Start(context.Context) error {
	f.starts++
	if len(f.startErrors) >= f.starts {
		return f.startErrors[f.starts-1]
	}

	return nil
}

// A restart failure followed by a failed restore reports both reasons, and the value the
// request asked for is never persisted. The requested value differs from the saved one, so
// the persisted-value assertion can tell a correct rollback from a no-op commit.
func TestUpdateWebRTCConfigReportsRestartAndRestoreFailures(t *testing.T) {
	repository := isolatedRepository(t)
	before := repository.GetWebRTCConfig()
	requested := before.SignalingRateLimit + 15
	if requested == before.SignalingRateLimit {
		t.Fatal("the requested value must differ from the saved one")
	}

	applier := &failingApplier{
		config: before,
		// The two messages must not contain one another, or the assertion on the restart
		// reason could be satisfied by the rollback reason.
		stopErrors:  []error{errors.New("restart phase rejected"), errors.New("rollback phase rejected")},
		startErrors: []error{errors.New("restart start rejected"), errors.New("rollback start rejected")},
	}

	admin := &Admin{configRepository: repository, webrtcApplier: applier, cfg: &config.Config{}}

	body := fmt.Sprintf("{\"signalingRateLimit\":%d}", requested)
	request := httptest.NewRequest(http.MethodPost, "/api/admin/config/webrtc", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	admin.UpdateWebRTCConfig(recorder, request)

	// The application stage really received the requested value.
	if len(applier.applied) == 0 || applier.applied[0].SignalingRateLimit != requested {
		t.Fatalf("applied configurations = %+v, want the first one to carry %d", applier.applied, requested)
	}

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d (body %s)", recorder.Code, http.StatusConflict, recorder.Body.String())
	}

	responseBody := recorder.Body.String()
	if !strings.Contains(responseBody, "restart phase rejected") {
		t.Fatalf("body = %q, want the restart failure", responseBody)
	}
	if !strings.Contains(responseBody, "rollback phase rejected") {
		t.Fatalf("body = %q, want the rollback failure", responseBody)
	}
	if !strings.Contains(responseBody, "may not match the saved configuration") {
		t.Fatalf("body = %q, want the inconsistency note", responseBody)
	}

	if got := repository.GetWebRTCConfig().SignalingRateLimit; got != before.SignalingRateLimit {
		t.Fatalf("saved signaling rate limit = %d, want the previous value %d", got, before.SignalingRateLimit)
	}
}
