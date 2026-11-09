package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/owncast/owncast/utils"
	"github.com/owncast/owncast/webserver/router/middleware"
	webutils "github.com/owncast/owncast/webserver/utils"
)

// GetStatus gets the status of the server.
func (h *Handlers) GetStatus(w http.ResponseWriter, r *http.Request) {
	response := h.getStatusResponse()

	w.Header().Set("Content-Type", "application/json")
	middleware.DisableCache(w)

	if err := json.NewEncoder(w).Encode(response); err != nil {
		webutils.InternalErrorHandler(w, err)
	}
}

func (h *Handlers) getStatusResponse() webStatusResponse {
	status := h.stream.GetStatus()
	response := webStatusResponse{
		Online:             status.Online,
		ServerTime:         time.Now(),
		LastConnectTime:    status.LastConnectTime,
		LastDisconnectTime: status.LastDisconnectTime,
		VersionNumber:      status.VersionNumber,
		StreamTitle:        status.StreamTitle,
		InputProtocol:      status.InputProtocol,
		VideoBitrate:       status.VideoBitrate,
		AudioBitrate:       status.AudioBitrate,
		VideoPipeline:      status.VideoPipeline,
	}
	if !h.configRepository.GetHideViewerCount() {
		response.ViewerCount = status.ViewerCount
	}
	return response
}

type webStatusResponse struct {
	ServerTime         time.Time       `json:"serverTime"`
	LastConnectTime    *utils.NullTime `json:"lastConnectTime"`
	LastDisconnectTime *utils.NullTime `json:"lastDisconnectTime"`

	VersionNumber string `json:"versionNumber"`
	StreamTitle   string `json:"streamTitle"`
	ViewerCount   int    `json:"viewerCount,omitempty"`
	Online        bool   `json:"online"`
	// InputProtocol is additive: "rtmp" or "srt" while a publisher is
	// connected, omitted otherwise (spec 7.2).
	InputProtocol string `json:"inputProtocol,omitempty"`
	// Additive stream metering for the viewer Stats for nerds panel.
	VideoBitrate  int    `json:"videoBitrate,omitempty"`
	AudioBitrate  int    `json:"audioBitrate,omitempty"`
	VideoPipeline string `json:"videoPipeline,omitempty"`
}
