package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/owncast/owncast/models"
)

// Ping is fired by a client to show they are still an active viewer. It also
// carries the playback details the server cannot measure itself (protocol,
// quality, latency, display name) as optional query parameters, so a player can
// report more without a second endpoint or a breaking API change.
func (h *Handlers) Ping(w http.ResponseWriter, r *http.Request) {
	viewer := models.GenerateViewerFromRequest(r)
	applyViewerReport(&viewer, r)
	h.stream.SetViewerActive(&viewer)
	w.WriteHeader(http.StatusOK)
}

// applyViewerReport copies the optional heartbeat parameters onto the viewer.
// Unknown or malformed values are ignored rather than rejected: a stale player
// must never be able to break the viewer count.
func applyViewerReport(viewer *models.Viewer, r *http.Request) {
	query := r.URL.Query()

	if protocol := strings.ToLower(strings.TrimSpace(query.Get("protocol"))); protocol == "hls" || protocol == "webrtc" {
		viewer.Protocol = protocol
	}

	viewer.ReportedToken = truncateReportString(query.Get("token"), 128)
	viewer.DisplayName = truncateReportString(query.Get("name"), 64)
	viewer.ReportedQuality = truncateReportString(query.Get("quality"), 32)

	if value, err := strconv.ParseFloat(query.Get("latency"), 64); err == nil && value >= 0 && value < 600000 {
		viewer.ReportedLatency = &value
	}

	if resolution := truncateReportString(query.Get("resolution"), 16); resolution != "" {
		viewer.Resolution = resolution
	}

	if value, err := strconv.ParseFloat(query.Get("fps"), 64); err == nil && value > 0 && value < 1000 {
		viewer.FPS = &value
	}

	if value, err := strconv.ParseFloat(query.Get("bitrate"), 64); err == nil && value > 0 && value < 1000000 {
		viewer.BitrateKbps = &value
	}
}

// truncateReportString bounds a client-supplied string so a hostile or buggy
// player cannot store unbounded text in the viewer table.
func truncateReportString(value string, limit int) string {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) > limit {
		return trimmed[:limit]
	}

	return trimmed
}
