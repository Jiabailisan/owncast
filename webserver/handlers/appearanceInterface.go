package handlers

// Delegations for the Phase 5 appearance and WebRTC stats-beacon endpoints.
// Public beacons are rate-limited inside the admin handlers; the path setters
// use the same admin auth + CSRF gate as the other configuration endpoints.

import "net/http"

// ReportCustomPlayerError records a custom player module load failure (public).
func (s *ServerInterfaceImpl) ReportCustomPlayerError(w http.ResponseWriter, r *http.Request) {
	s.h.admin.ReportCustomPlayerError(w, r)
}

// ReportCustomPlayerErrorOptions is the CORS preflight for ReportCustomPlayerError.
func (s *ServerInterfaceImpl) ReportCustomPlayerErrorOptions(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

// ReportWebRTCClientReport accepts browser-measured WebRTC stats (public).
func (s *ServerInterfaceImpl) ReportWebRTCClientReport(w http.ResponseWriter, r *http.Request) {
	s.h.admin.ReportWebRTCClientReport(w, r)
}

// ReportWebRTCClientReportOptions is the CORS preflight for ReportWebRTCClientReport.
func (s *ServerInterfaceImpl) ReportWebRTCClientReportOptions(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

// SetCustomPlayerPath validates and stores the custom player module path.
func (s *ServerInterfaceImpl) SetCustomPlayerPath(w http.ResponseWriter, r *http.Request) {
	s.h.middleware.RequireAdminAuth(s.h.admin.SetCustomPlayerPath)(w, r)
}

// SetCustomPlayerPathOptions is the CORS preflight for SetCustomPlayerPath.
func (s *ServerInterfaceImpl) SetCustomPlayerPathOptions(w http.ResponseWriter, r *http.Request) {
	s.h.middleware.RequireAdminAuth(s.h.admin.SetCustomPlayerPath)(w, r)
}

// SetCustomFrontendDir validates and stores the custom viewer frontend directory.
func (s *ServerInterfaceImpl) SetCustomFrontendDir(w http.ResponseWriter, r *http.Request) {
	s.h.middleware.RequireAdminAuth(s.h.admin.SetCustomFrontendDir)(w, r)
}

// SetCustomFrontendDirOptions is the CORS preflight for SetCustomFrontendDir.
func (s *ServerInterfaceImpl) SetCustomFrontendDirOptions(w http.ResponseWriter, r *http.Request) {
	s.h.middleware.RequireAdminAuth(s.h.admin.SetCustomFrontendDir)(w, r)
}
