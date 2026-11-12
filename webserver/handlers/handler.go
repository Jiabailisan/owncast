package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/owncast/owncast/webserver/handlers/generated"
	"github.com/owncast/owncast/webserver/router/middleware"
)

// ServerInterfaceImpl is the OpenAPI-generated ServerInterface
// implementation. It holds the dependency-bearing handler sets and
// delegates each generated method to either a free function in this
// package or a method on one of those sets.
//
// As more handlers migrate to needing injected services, their
// delegations here switch from `pkg.X(w, r)` to `s.h.X(w, r)` or
// `s.h.admin.X(w, r)`.
type ServerInterfaceImpl struct {
	h *Handlers
}

// ensure ServerInterfaceImpl implements ServerInterface.
var _ generated.ServerInterface = &ServerInterfaceImpl{}

func New(h *Handlers) *ServerInterfaceImpl {
	return &ServerInterfaceImpl{h: h}
}

func (s *ServerInterfaceImpl) Handler() http.Handler {
	return generated.HandlerWithOptions(s, generated.ChiServerOptions{
		ErrorHandlerFunc: writeParameterError,
	})
}

// writeParameterError answers a request the generated binder rejected. Without it a
// malformed parameter on an admin route was answered with a bare 400 before the
// admin authentication ran, so an unauthenticated caller learned that the endpoint
// exists and got a text/plain body that is not part of the API schema. Admin routes
// answer 401 first now, and everything else gets a JSON 400.
func writeParameterError(w http.ResponseWriter, r *http.Request, err error) {
	if strings.HasPrefix(r.URL.Path, "/api/admin/") {
		if _, _, ok := r.BasicAuth(); !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="Owncast"`)
			w.WriteHeader(http.StatusUnauthorized)

			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": false,
		"message": "invalid request parameters: " + err.Error(),
	})
}

func (s *ServerInterfaceImpl) GetStatus(w http.ResponseWriter, r *http.Request) {
	s.h.GetStatus(w, r)
}

func (*ServerInterfaceImpl) GetCustomEmojiList(w http.ResponseWriter, r *http.Request) {
	GetCustomEmojiList(w, r)
}

func (s *ServerInterfaceImpl) GetChatMessages(w http.ResponseWriter, r *http.Request, params generated.GetChatMessagesParams) {
	s.h.middleware.RequireUserAccessToken(s.h.GetChatMessages)(w, r)
}

func (s *ServerInterfaceImpl) RegisterAnonymousChatUser(w http.ResponseWriter, r *http.Request, params generated.RegisterAnonymousChatUserParams) {
	s.h.RegisterAnonymousChatUser(w, r)
}

func (s *ServerInterfaceImpl) RegisterAnonymousChatUserOptions(w http.ResponseWriter, r *http.Request) {
	s.h.RegisterAnonymousChatUser(w, r)
}

func (s *ServerInterfaceImpl) UpdateMessageVisibility(w http.ResponseWriter, r *http.Request, params generated.UpdateMessageVisibilityParams) {
	s.h.middleware.RequireUserModerationScopeAccesstoken(s.h.admin.UpdateMessageVisibility)(w, r)
}

func (s *ServerInterfaceImpl) UpdateUserEnabled(w http.ResponseWriter, r *http.Request, params generated.UpdateUserEnabledParams) {
	s.h.middleware.RequireUserModerationScopeAccesstoken(s.h.admin.UpdateUserEnabled)(w, r)
}

func (s *ServerInterfaceImpl) GetWebConfig(w http.ResponseWriter, r *http.Request) {
	s.h.GetWebConfig(w, r)
}

func (s *ServerInterfaceImpl) GetYPResponse(w http.ResponseWriter, r *http.Request) {
	s.h.yp.GetYPResponse(w, r)
}

func (*ServerInterfaceImpl) GetAllSocialPlatforms(w http.ResponseWriter, r *http.Request) {
	GetAllSocialPlatforms(w, r)
}

func (s *ServerInterfaceImpl) GetVideoStreamOutputVariants(w http.ResponseWriter, r *http.Request) {
	s.h.GetVideoStreamOutputVariants(w, r)
}

func (s *ServerInterfaceImpl) Ping(w http.ResponseWriter, r *http.Request, _ generated.PingParams) {
	// The report parameters are documented for clients but parsed from the query
	// string by the handler, which also accepts the token as a header.
	s.h.Ping(w, r)
}

func (s *ServerInterfaceImpl) RemoteFollow(w http.ResponseWriter, r *http.Request) {
	s.h.RemoteFollow(w, r)
}

func (s *ServerInterfaceImpl) GetFollowers(w http.ResponseWriter, r *http.Request, params generated.GetFollowersParams) {
	middleware.HandlePagination(s.h.GetFollowers)(w, r)
}

func (s *ServerInterfaceImpl) ReportPlaybackMetrics(w http.ResponseWriter, r *http.Request) {
	s.h.ReportPlaybackMetrics(w, r)
}

func (s *ServerInterfaceImpl) ReportCmcd(w http.ResponseWriter, r *http.Request) {
	s.h.ReportCmcd(w, r)
}

func (s *ServerInterfaceImpl) ReportCmcdQuery(w http.ResponseWriter, r *http.Request, params generated.ReportCmcdQueryParams) {
	s.h.ReportCmcd(w, r)
}

func (s *ServerInterfaceImpl) ReportCmcdOptions(w http.ResponseWriter, r *http.Request) {
	s.h.ReportCmcd(w, r)
}

func (s *ServerInterfaceImpl) RegisterForLiveNotifications(w http.ResponseWriter, r *http.Request, params generated.RegisterForLiveNotificationsParams) {
	s.h.middleware.RequireUserAccessToken(s.h.RegisterForLiveNotifications)(w, r)
}

// Federated servers endpoints

func (s *ServerInterfaceImpl) GetFederatedServers(w http.ResponseWriter, r *http.Request) {
	s.h.admin.GetFederatedServers(w, r)
}

func (s *ServerInterfaceImpl) GetAdminFederatedServers(w http.ResponseWriter, r *http.Request) {
	s.h.middleware.RequireAdminAuth(s.h.admin.GetAdminFederatedServers)(w, r)
}

func (s *ServerInterfaceImpl) AddFederatedServer(w http.ResponseWriter, r *http.Request) {
	s.h.middleware.RequireAdminAuth(s.h.admin.AddFederatedServer)(w, r)
}

func (s *ServerInterfaceImpl) AddFederatedServerOptions(w http.ResponseWriter, r *http.Request) {
	s.h.admin.AddFederatedServerOptions(w, r)
}

func (s *ServerInterfaceImpl) RemoveFederatedServer(w http.ResponseWriter, r *http.Request, id int) {
	s.h.middleware.RequireAdminAuth(func(w http.ResponseWriter, r *http.Request) {
		s.h.admin.RemoveFederatedServer(w, r, id)
	})(w, r)
}

func (s *ServerInterfaceImpl) RemoveFederatedServerOptions(w http.ResponseWriter, r *http.Request, id int) {
	s.h.admin.RemoveFederatedServerOptions(w, r, id)
}

func (s *ServerInterfaceImpl) ReorderFederatedServer(w http.ResponseWriter, r *http.Request, id int) {
	s.h.middleware.RequireAdminAuth(func(w http.ResponseWriter, r *http.Request) {
		s.h.admin.ReorderFederatedServer(w, r, id)
	})(w, r)
}

func (s *ServerInterfaceImpl) ReorderFederatedServerOptions(w http.ResponseWriter, r *http.Request, id int) {
	s.h.admin.ReorderFederatedServerOptions(w, r, id)
}

func (s *ServerInterfaceImpl) GetFeatureRequests(w http.ResponseWriter, r *http.Request) {
	s.h.middleware.RequireAdminAuth(s.h.admin.GetFeatureRequests)(w, r)
}
