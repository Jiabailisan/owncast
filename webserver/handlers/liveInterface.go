package handlers

// Delegations for the Phase 4 admin Live Stream Status endpoints. The
// OpenAPI-generated ServerInterface requires one method per operation; each
// wraps the real admin handler with the shared admin auth (+CSRF) gate, like
// the configuration endpoints in configInterface.go and
// liveStreamingConfigInterface.go.

import (
	"net/http"

	"github.com/owncast/owncast/webserver/handlers/generated"
)

// GetLiveStatus returns the aggregated Live Stream Status snapshot.
func (s *ServerInterfaceImpl) GetLiveStatus(w http.ResponseWriter, r *http.Request) {
	s.h.middleware.RequireAdminAuth(s.h.admin.GetLiveStatus)(w, r)
}

// GetLiveViewers lists the current live viewers.
func (s *ServerInterfaceImpl) GetLiveViewers(w http.ResponseWriter, r *http.Request, params generated.GetLiveViewersParams) {
	page, pageSize := 0, 0
	if params.Page != nil {
		page = *params.Page
	}
	if params.PageSize != nil {
		pageSize = *params.PageSize
	}
	sortBy, order, protocol := "", "", ""
	if params.Sort != nil {
		sortBy = string(*params.Sort)
	}
	if params.Order != nil {
		order = string(*params.Order)
	}
	if params.Protocol != nil {
		protocol = string(*params.Protocol)
	}
	query := ""
	if params.Q != nil {
		query = *params.Q
	}

	handler := func(w http.ResponseWriter, r *http.Request) {
		s.h.admin.GetLiveViewers(w, r, page, pageSize, sortBy, order, protocol, query)
	}
	s.h.middleware.RequireAdminAuth(handler)(w, r)
}

// GetLiveFFmpeg returns the FFmpeg processes feeding the live stream.
func (s *ServerInterfaceImpl) GetLiveFFmpeg(w http.ResponseWriter, r *http.Request) {
	s.h.middleware.RequireAdminAuth(s.h.admin.GetLiveFFmpeg)(w, r)
}
