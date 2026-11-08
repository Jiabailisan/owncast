package admin

// Admin output settings API (spec 3.4/4.4): the FFmpeg process model.
//
// The setting is applyMode restart-stream: a saved value takes effect at the
// next stream start and is never applied to the running stream, so the API
// reports pendingRestart instead of claiming a live apply.

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/owncast/owncast/config/settings"
	"github.com/owncast/owncast/models"
)

// outputConfig is the persisted output settings object.
type outputConfig struct {
	ProcessModel string `json:"processModel"`
}

// outputConfigState is the GET response: current config, defaults and pending restart.
type outputConfigState struct {
	Config         outputConfig   `json:"config"`
	Defaults       outputConfig   `json:"defaults"`
	PendingRestart pendingRestart `json:"pendingRestart"`
}

// outputConfigUpdateRequest is the raw PUT body.
type outputConfigUpdateRequest struct {
	ProcessModel string `json:"processModel"`
}

// newOutputConfigState builds the response payload for a process model value.
func newOutputConfigState(processModel string, pendingFields []string) outputConfigState {
	return outputConfigState{
		Config:         outputConfig{ProcessModel: processModel},
		Defaults:       outputConfig{ProcessModel: defaultValueProcessModel()},
		PendingRestart: newPendingRestart(pendingFields),
	}
}

// defaultValueProcessModel returns the metadata default process model.
func defaultValueProcessModel() string {
	field, ok := settings.FieldByKey(settings.OutputMetadata(), "processModel")
	if !ok {
		return models.OutputProcessModelShared
	}
	if value, ok := field.Default.(string); ok {
		return value
	}
	return models.OutputProcessModelShared
}

// GetOutputConfig returns the persisted output process model, the default and
// the pending restart state.
func (a *Admin) GetOutputConfig(w http.ResponseWriter, _ *http.Request) {
	processModel := a.configRepository.GetOutputProcessModel()
	state := newOutputConfigState(processModel, nil)
	if a.stream != nil && a.stream.GetStatus().Online {
		// The live stream still runs the previous process model; the saved value
		// applies at the next stream start, so it stays pending while live.
		state.PendingRestart = newPendingRestart([]string{fieldNameProcessModel})
	}
	writeLiveConfigJSON(w, state)
}

// UpdateOutputConfig validates and persists the FFmpeg process model.
func (a *Admin) UpdateOutputConfig(w http.ResponseWriter, r *http.Request) {
	body, ok := readConfigBody(w, r)
	if !ok {
		return
	}

	request := outputConfigUpdateRequest{}
	if err := json.Unmarshal(body, &request); err != nil {
		writeLiveConfigBadRequest(w, "unable to parse output configuration: "+err.Error())
		return
	}
	if !settings.IsValidOutputProcessModel(request.ProcessModel) {
		writeLiveConfigValidationErrors(w, settings.FieldErrors{{
			Field:   "processModel",
			Message: "must be one of " + strings.Join(settings.OutputProcessModelValues(), ", "),
		}})
		return
	}

	previous := a.configRepository.GetOutputProcessModel()
	if err := a.configRepository.SetOutputProcessModel(request.ProcessModel); err != nil {
		writeLiveConfigInternalError(w, "unable to persist output configuration: "+err.Error())
		return
	}

	// restart-stream: the change applies at the next stream start. While a
	// stream is live the running stream keeps the previous model, so report it
	// as pending rather than implying a live apply.
	pendingFields := []string{}
	if a.stream != nil && a.stream.GetStatus().Online && previous != request.ProcessModel {
		pendingFields = []string{fieldNameProcessModel}
	}

	state := newOutputConfigState(request.ProcessModel, pendingFields)
	response := map[string]any{
		responseKeySuccess:        true,
		responseKeyConfig:         state.Config,
		responseKeyDefaults:       state.Defaults,
		responseKeyPendingRestart: state.PendingRestart,
	}
	writeLiveConfigJSON(w, response)
}
