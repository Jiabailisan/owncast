package handlers

// Delegations for the Phase 2 SRT/WebRTC configuration endpoints. The
// OpenAPI-generated ServerInterface requires one method per operation; each
// one wraps the real admin handler with the shared admin auth (+CSRF) gate,
// exactly like the existing configuration endpoints in configInterface.go.

import "net/http"

// GetSettingsMetadata returns the settings metadata registry.
func (s *ServerInterfaceImpl) GetSettingsMetadata(w http.ResponseWriter, r *http.Request) {
	s.h.middleware.RequireAdminAuth(s.h.admin.GetSettingsMetadata)(w, r)
}

// GetSettingsMetadataOptions is the CORS preflight for GetSettingsMetadata.
func (s *ServerInterfaceImpl) GetSettingsMetadataOptions(w http.ResponseWriter, r *http.Request) {
	s.h.middleware.RequireAdminAuth(s.h.admin.GetSettingsMetadata)(w, r)
}

// GetSRTConfig returns the SRT configuration state.
func (s *ServerInterfaceImpl) GetSRTConfig(w http.ResponseWriter, r *http.Request) {
	s.h.middleware.RequireAdminAuth(s.h.admin.GetSRTConfig)(w, r)
}

// GetSRTConfigOptions is the CORS preflight for GetSRTConfig.
func (s *ServerInterfaceImpl) GetSRTConfigOptions(w http.ResponseWriter, r *http.Request) {
	s.h.middleware.RequireAdminAuth(s.h.admin.GetSRTConfig)(w, r)
}

// UpdateSRTConfig applies and persists an SRT configuration update.
func (s *ServerInterfaceImpl) UpdateSRTConfig(w http.ResponseWriter, r *http.Request) {
	s.h.middleware.RequireAdminAuth(s.h.admin.UpdateSRTConfig)(w, r)
}

// UpdateSRTConfigOptions is the CORS preflight for UpdateSRTConfig.
func (s *ServerInterfaceImpl) UpdateSRTConfigOptions(w http.ResponseWriter, r *http.Request) {
	s.h.middleware.RequireAdminAuth(s.h.admin.UpdateSRTConfig)(w, r)
}

// GetWebRTCConfig returns the WebRTC configuration state.
func (s *ServerInterfaceImpl) GetWebRTCConfig(w http.ResponseWriter, r *http.Request) {
	s.h.middleware.RequireAdminAuth(s.h.admin.GetWebRTCConfig)(w, r)
}

// GetWebRTCConfigOptions is the CORS preflight for GetWebRTCConfig.
func (s *ServerInterfaceImpl) GetWebRTCConfigOptions(w http.ResponseWriter, r *http.Request) {
	s.h.middleware.RequireAdminAuth(s.h.admin.GetWebRTCConfig)(w, r)
}

// UpdateWebRTCConfig applies and persists a WebRTC configuration update.
func (s *ServerInterfaceImpl) UpdateWebRTCConfig(w http.ResponseWriter, r *http.Request) {
	s.h.middleware.RequireAdminAuth(s.h.admin.UpdateWebRTCConfig)(w, r)
}

// UpdateWebRTCConfigOptions is the CORS preflight for UpdateWebRTCConfig.
func (s *ServerInterfaceImpl) UpdateWebRTCConfigOptions(w http.ResponseWriter, r *http.Request) {
	s.h.middleware.RequireAdminAuth(s.h.admin.UpdateWebRTCConfig)(w, r)
}

// RunConfigPreflight probes FFmpeg capabilities and configured port availability.
func (s *ServerInterfaceImpl) RunConfigPreflight(w http.ResponseWriter, r *http.Request) {
	s.h.middleware.RequireAdminAuth(s.h.admin.RunConfigPreflight)(w, r)
}

// RunConfigPreflightOptions is the CORS preflight for RunConfigPreflight.
func (s *ServerInterfaceImpl) RunConfigPreflightOptions(w http.ResponseWriter, r *http.Request) {
	s.h.middleware.RequireAdminAuth(s.h.admin.RunConfigPreflight)(w, r)
}

// TestWebRTCDomain resolves the WebRTC domain and reports advertised addresses.
func (s *ServerInterfaceImpl) TestWebRTCDomain(w http.ResponseWriter, r *http.Request) {
	s.h.middleware.RequireAdminAuth(s.h.admin.TestWebRTCDomain)(w, r)
}

// TestWebRTCDomainOptions is the CORS preflight for TestWebRTCDomain.
func (s *ServerInterfaceImpl) TestWebRTCDomainOptions(w http.ResponseWriter, r *http.Request) {
	s.h.middleware.RequireAdminAuth(s.h.admin.TestWebRTCDomain)(w, r)
}
