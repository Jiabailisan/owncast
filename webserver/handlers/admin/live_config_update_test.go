package admin

import (
	"context"
	"errors"
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
}

func (f *failingApplier) Config() models.WebRTCConfig { return f.config }

func (f *failingApplier) UpdateConfig(config models.WebRTCConfig) ([]string, error) {
	f.config = config

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

// A restart failure followed by a failed restore reports both reasons and leaves the
// saved configuration untouched.
func TestUpdateWebRTCConfigReportsRestartAndRestoreFailures(t *testing.T) {
	repository := isolatedRepository(t)
	before := repository.GetWebRTCConfig()

	applier := &failingApplier{
		config:      before,
		stopErrors:  []error{errors.New("stop failed"), errors.New("restore stop failed")},
		startErrors: []error{errors.New("start failed"), errors.New("restore start failed")},
	}

	admin := &Admin{configRepository: repository, webrtcApplier: applier, cfg: &config.Config{}}

	request := httptest.NewRequest(http.MethodPost, "/api/admin/config/webrtc", strings.NewReader("{}"))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	admin.UpdateWebRTCConfig(recorder, request)

	body := recorder.Body.String()
	if !strings.Contains(body, "stop failed") {
		t.Fatalf("body = %q, want the restart failure", body)
	}
	if !strings.Contains(body, "restore stop failed") {
		t.Fatalf("body = %q, want the restore failure", body)
	}
	if !strings.Contains(body, "may not match the saved configuration") {
		t.Fatalf("body = %q, want the inconsistency note", body)
	}
	if got := repository.GetWebRTCConfig().SignalingRateLimit; got != before.SignalingRateLimit {
		t.Fatalf("saved signaling rate limit = %d, want the previous value %d", got, before.SignalingRateLimit)
	}
}
