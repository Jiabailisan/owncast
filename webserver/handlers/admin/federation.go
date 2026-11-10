package admin

import (
	"errors"
	"net/http"

	webutils "github.com/owncast/owncast/webserver/utils"
)

// SendFederatedMessage will send a manual message to the fediverse.
func (a *Admin) SendFederatedMessage(w http.ResponseWriter, r *http.Request) {
	if !requirePOST(w, r) {
		return
	}

	if !a.configRepository.GetFederationEnabled() {
		webutils.WriteSimpleResponse(w, false, "Federation is disabled")
		return
	}

	configValue, success := getValueFromRequest(w, r)
	if !success {
		return
	}

	message, ok := valueAsStringOK(configValue.Value)
	if !ok {
		webutils.WriteSimpleResponse(w, false, "unable to send message")
		return
	}

	if err := a.activitypub.SendPublicFederatedMessage(message); err != nil {
		webutils.WriteSimpleResponse(w, false, err.Error())
		return
	}

	webutils.WriteSimpleResponse(w, true, "sent")
}

// SetFederationEnabled will set if Federation features are enabled.
func (a *Admin) SetFederationEnabled(w http.ResponseWriter, r *http.Request) {
	if !requirePOST(w, r) {
		return
	}

	configValue, success := getValueFromRequest(w, r)
	if !success {
		return
	}

	typedValue, valueOK := valueAsBoolOK(configValue.Value)
	if !valueOK {
		webutils.BadRequestHandler(w, errors.New("value must be a boolean"))
		return
	}
	if err := a.configRepository.SetFederationEnabled(typedValue); err != nil {
		webutils.WriteSimpleResponse(w, false, err.Error())
		return
	}
	webutils.WriteSimpleResponse(w, true, "federation features saved")
}

// SetFederationActivityPrivate will set if Federation features are private to followers.
func (a *Admin) SetFederationActivityPrivate(w http.ResponseWriter, r *http.Request) {
	if !requirePOST(w, r) {
		return
	}

	configValue, success := getValueFromRequest(w, r)
	if !success {
		return
	}

	typedValue, valueOK := valueAsBoolOK(configValue.Value)
	if !valueOK {
		webutils.BadRequestHandler(w, errors.New("value must be a boolean"))
		return
	}
	if err := a.configRepository.SetFederationIsPrivate(typedValue); err != nil {
		webutils.WriteSimpleResponse(w, false, err.Error())
		return
	}

	// Update Fediverse followers about this change.
	if err := a.activitypub.UpdateFollowersWithAccountUpdates(); err != nil {
		webutils.WriteSimpleResponse(w, false, err.Error())
		return
	}

	webutils.WriteSimpleResponse(w, true, "federation private saved")
}

// SetFederationShowEngagement will set if Fedivese engagement shows in chat.
func (a *Admin) SetFederationShowEngagement(w http.ResponseWriter, r *http.Request) {
	if !requirePOST(w, r) {
		return
	}

	configValue, success := getValueFromRequest(w, r)
	if !success {
		return
	}

	typedValue, valueOK := valueAsBoolOK(configValue.Value)
	if !valueOK {
		webutils.BadRequestHandler(w, errors.New("value must be a boolean"))
		return
	}
	if err := a.configRepository.SetFederationShowEngagement(typedValue); err != nil {
		webutils.WriteSimpleResponse(w, false, err.Error())
		return
	}
	webutils.WriteSimpleResponse(w, true, "federation show engagement saved")
}

// SetFederationEnableQuotes will set if posts from this server can be quoted.
func (a *Admin) SetFederationEnableQuotes(w http.ResponseWriter, r *http.Request) {
	if !requirePOST(w, r) {
		return
	}

	configValue, success := getValueFromRequest(w, r)
	if !success {
		return
	}

	enabled, ok := valueAsBoolOK(configValue.Value)
	if !ok {
		webutils.WriteSimpleResponse(w, false, "invalid value for federation enable quotes")
		return
	}

	if err := a.configRepository.SetFederationEnableQuotes(enabled); err != nil {
		webutils.WriteSimpleResponse(w, false, err.Error())
		return
	}
	webutils.WriteSimpleResponse(w, true, "federation enable quotes saved")
}

// SetFederationHideFollowersTab will set if the followers tab is hidden on the public web UI.
func (a *Admin) SetFederationHideFollowersTab(w http.ResponseWriter, r *http.Request) {
	if !requirePOST(w, r) {
		return
	}

	configValue, success := getValueFromRequest(w, r)
	if !success {
		return
	}

	typedValue, valueOK := valueAsBoolOK(configValue.Value)
	if !valueOK {
		webutils.BadRequestHandler(w, errors.New("value must be a boolean"))
		return
	}
	if err := a.configRepository.SetFederationHideFollowersTab(typedValue); err != nil {
		webutils.WriteSimpleResponse(w, false, err.Error())
		return
	}
	webutils.WriteSimpleResponse(w, true, "federation hide followers tab saved")
}

// SetFederationUsername will set the local actor username used for federation activities.
func (a *Admin) SetFederationUsername(w http.ResponseWriter, r *http.Request) {
	if !requirePOST(w, r) {
		return
	}

	configValue, success := getValueFromRequest(w, r)
	if !success {
		return
	}

	typedValue, valueOK := valueAsStringOK(configValue.Value)
	if !valueOK {
		webutils.BadRequestHandler(w, errors.New("value must be a string"))
		return
	}
	if err := a.configRepository.SetFederationUsername(typedValue); err != nil {
		webutils.WriteSimpleResponse(w, false, err.Error())
		return
	}

	webutils.WriteSimpleResponse(w, true, "username saved")
}

// SetFederationGoLiveMessage will set the federated message sent when the streamer goes live.
func (a *Admin) SetFederationGoLiveMessage(w http.ResponseWriter, r *http.Request) {
	if !requirePOST(w, r) {
		return
	}

	configValue, success := getValueFromRequest(w, r)
	if !success {
		return
	}

	typedValue, valueOK := valueAsStringOK(configValue.Value)
	if !valueOK {
		webutils.BadRequestHandler(w, errors.New("value must be a string"))
		return
	}
	if err := a.configRepository.SetFederationGoLiveMessage(typedValue); err != nil {
		webutils.WriteSimpleResponse(w, false, err.Error())
		return
	}

	webutils.WriteSimpleResponse(w, true, "message saved")
}

// SetFederationBlockDomains saves a list of domains to block on the Fediverse.
func (a *Admin) SetFederationBlockDomains(w http.ResponseWriter, r *http.Request) {
	if !requirePOST(w, r) {
		return
	}

	configValues, success := getValuesFromRequest(w, r)
	if !success {
		webutils.WriteSimpleResponse(w, false, "unable to handle provided domains")
		return
	}

	domainStrings := make([]string, 0)
	for _, domain := range configValues {
		domainStrings = append(domainStrings, valueAsString(domain.Value))
	}

	if err := a.configRepository.SetBlockedFederatedDomains(domainStrings); err != nil {
		webutils.WriteSimpleResponse(w, false, err.Error())
		return
	}

	webutils.WriteSimpleResponse(w, true, "saved")
}

// GetFederatedActions will return the saved list of accepted inbound
// federated activities.
func (a *Admin) GetFederatedActions(page int, pageSize int, w http.ResponseWriter, r *http.Request) {
	offset := pageSize * page

	activities, total, err := a.activitypub.GetInboundActivities(pageSize, offset)
	if err != nil {
		webutils.WriteSimpleResponse(w, false, err.Error())
		return
	}

	response := webutils.PaginatedResponse{
		Total:   total,
		Results: activities,
	}

	webutils.WriteResponse(w, response)
}
