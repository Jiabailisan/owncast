package handlers

// Delegations for the output settings (FFmpeg process model) API. It uses the
// same admin auth + CSRF gate as the other configuration mutations.

import "net/http"

// GetOutputConfig returns the persisted output settings state.
func (s *ServerInterfaceImpl) GetOutputConfig(w http.ResponseWriter, r *http.Request) {
	s.h.middleware.RequireAdminAuth(s.h.admin.GetOutputConfig)(w, r)
}

// GetOutputConfigOptions is the CORS preflight for GetOutputConfig.
func (s *ServerInterfaceImpl) GetOutputConfigOptions(w http.ResponseWriter, r *http.Request) {
	s.h.middleware.RequireAdminAuth(s.h.admin.GetOutputConfig)(w, r)
}

// UpdateOutputConfig validates and persists the FFmpeg process model.
func (s *ServerInterfaceImpl) UpdateOutputConfig(w http.ResponseWriter, r *http.Request) {
	s.h.middleware.RequireAdminAuth(s.h.admin.UpdateOutputConfig)(w, r)
}

// UpdateOutputConfigOptions is the CORS preflight for UpdateOutputConfig.
func (s *ServerInterfaceImpl) UpdateOutputConfigOptions(w http.ResponseWriter, r *http.Request) {
	s.h.middleware.RequireAdminAuth(s.h.admin.UpdateOutputConfig)(w, r)
}
