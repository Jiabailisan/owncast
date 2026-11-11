package admin

// Admin Live Stream Status API: the read-only endpoints backing the admin
// Live Streaming page and the admin menu live card (spec section 6). The data
// is aggregated by services/livestatus from the stream, SRT and WebRTC
// services; this file only negotiates the HTTP shape from
// docs/architecture/api-contract.md section 4 and applies the viewer-table
// parameter normalization. Every value is a point-in-time snapshot; nullable
// metrics stay null (the UI renders N/A and must never estimate).

import (
	"net/http"
	"strings"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/transcoder"
)

const (
	// defaultLiveViewersPageSize is the contract default for /live/viewers.
	defaultLiveViewersPageSize = 25
	// maxLiveViewersPageSize bounds a page; larger requests are clamped.
	maxLiveViewersPageSize = 200
	// minLiveViewersPage is the first valid page number.
	minLiveViewersPage = 1

	liveSortDuration    = "duration"
	liveSortConnectedAt = "connectedAt"
	liveSortProtocol    = "protocol"
	liveSortBitrate     = "bitrate"

	liveOrderAscending  = "asc"
	liveOrderDescending = "desc"

	liveProtocolFilterAll    = "all"
	liveProtocolFilterHLS    = "hls"
	liveProtocolFilterWebRTC = "webrtc"
)

// liveStatusPayload is the Live Stream Status snapshot plus the current SRT
// listener block. The embedded models.LiveStatus keeps the documented payload
// shape unchanged; "srt" is additive and mirrors GET /admin/config/srt's
// listener object so the admin can render SRT state on the offline page too.
type liveStatusPayload struct {
	models.LiveStatus
	SRT map[string]any `json:"srt"`
}

// GetLiveStatus returns the aggregated Live Stream Status snapshot.
func (a *Admin) GetLiveStatus(w http.ResponseWriter, _ *http.Request) {
	status := models.LiveStatus{
		State:    models.LiveStateOffline,
		Warnings: []models.StatusWarning{},
	}
	if a.liveStatus != nil {
		status = a.liveStatus.Status()
	}
	if status.Warnings == nil {
		status.Warnings = []models.StatusWarning{}
	}
	if status.WebRTC.Warnings == nil {
		status.WebRTC.Warnings = []models.StatusWarning{}
	}
	if status.FFmpeg.Processes == nil {
		status.FFmpeg.Processes = []models.FFmpegProcessStatus{}
	}
	if status.FFmpeg.ProcessModel == "" && a.configRepository != nil {
		status.FFmpeg.ProcessModel = a.configRepository.GetOutputProcessModel()
	}

	writeLiveConfigJSON(w, liveStatusPayload{
		LiveStatus: status,
		SRT:        a.srtListenerState(),
	})
}

// GetLiveViewers returns one normalized page of the admin live viewer table.
// Query parameters are normalized here (not rejected) so a stale or malformed
// admin URL still renders a deterministic default table.
func (a *Admin) GetLiveViewers(w http.ResponseWriter, _ *http.Request, page, pageSize int, sortBy, order, protocol, query string) {
	page, pageSize = normalizeLiveViewersPagination(page, pageSize)
	sortBy = normalizeLiveViewersSort(sortBy)
	order = normalizeLiveViewersOrder(order)
	protocol = normalizeLiveViewersProtocol(protocol)
	query = strings.TrimSpace(query)

	if a.liveStatus == nil {
		writeLiveConfigJSON(w, models.LiveViewersPage{
			Viewers:  []models.LiveViewer{},
			Page:     page,
			PageSize: pageSize,
		})
		return
	}

	writeLiveConfigJSON(w, a.liveStatus.Viewers(page, pageSize, sortBy, order, protocol, query))
}

// GetLiveFFmpeg returns the FFmpeg process model and the supervised processes
// feeding the active stream, with the exact argv split into display sections.
// Secrets are already redacted by services/stream; a defensive second pass
// here makes the endpoint safe even if a future provider forgets to redact.
func (a *Admin) GetLiveFFmpeg(w http.ResponseWriter, _ *http.Request) {
	processModel := ""
	if a.configRepository != nil {
		processModel = a.configRepository.GetOutputProcessModel()
	}

	processes := []models.FFmpegProcessStatus{}
	if a.stream != nil {
		if listed := a.stream.FFmpegProcesses(); listed != nil {
			processes = listed
		}
	}

	secrets := a.liveFFmpegSecrets()
	for index := range processes {
		process := &processes[index]
		if process.Purpose == nil {
			process.Purpose = []string{}
		}
		if len(secrets) > 0 && len(process.Argv) > 0 {
			process.Argv = transcoder.RedactArgv(process.Argv, secrets)
		}
		if process.Sections == nil {
			process.Sections = map[string][]string{}
		}
	}

	writeLiveConfigJSON(w, models.FFmpegStatus{
		ProcessModel: processModel,
		Processes:    processes,
	})
}

// liveFFmpegSecrets collects configured secrets that must never be echoed by
// the FFmpeg panel. It mirrors stream.configuredSecrets for defense in depth.
func (a *Admin) liveFFmpegSecrets() []string {
	if a.configRepository == nil {
		return nil
	}

	secrets := make([]string, 0, 4)
	srtConfig := a.configRepository.GetSRTConfig()
	if srtConfig.Passphrase != "" {
		secrets = append(secrets, srtConfig.Passphrase)
	}
	webrtcConfig := a.configRepository.GetWebRTCConfig()
	for _, server := range webrtcConfig.TURNServers {
		if server.Credential != "" {
			secrets = append(secrets, server.Credential)
		}
		if server.StaticSecret != "" {
			secrets = append(secrets, server.StaticSecret)
		}
	}
	return secrets
}

func normalizeLiveViewersPagination(page, pageSize int) (int, int) {
	if page < minLiveViewersPage {
		page = minLiveViewersPage
	}
	switch {
	case pageSize < 1:
		pageSize = defaultLiveViewersPageSize
	case pageSize > maxLiveViewersPageSize:
		pageSize = maxLiveViewersPageSize
	}
	return page, pageSize
}

func normalizeLiveViewersSort(sortBy string) string {
	switch sortBy {
	case liveSortDuration, liveSortConnectedAt, liveSortProtocol, liveSortBitrate:
		return sortBy
	default:
		return liveSortConnectedAt
	}
}

func normalizeLiveViewersOrder(order string) string {
	if order == liveOrderAscending {
		return liveOrderAscending
	}
	return liveOrderDescending
}

func normalizeLiveViewersProtocol(protocol string) string {
	switch protocol {
	case liveProtocolFilterHLS, liveProtocolFilterWebRTC:
		return protocol
	default:
		return liveProtocolFilterAll
	}
}
