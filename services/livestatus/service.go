// Package livestatus aggregates a read-only point-in-time snapshot of the live
// stream for the admin Live Stream Status API (spec section 6). It has no
// lifecycle of its own and holds no state: every call reads the current state
// from the injected providers.
package livestatus

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/stream"
)

// ConfigProvider is the narrow read-only configuration surface needed here.
type ConfigProvider interface {
	GetWebRTCConfig() models.WebRTCConfig
	GetOutputProcessModel() string
}

// WebRTCProvider is the read-only view of the WebRTC delivery service.
// It is nil while WebRTC delivery is disabled or not wired.
type WebRTCProvider interface {
	// ViewerCount returns the number of active WHEP viewers.
	ViewerCount() int
	// LiveViewers returns one row per active WHEP viewer. Nullable metrics
	// stay nil when the server cannot measure them.
	LiveViewers() []models.LiveViewer
	// Warnings returns operator-visible configuration/runtime warnings.
	Warnings() []string
}

// GeoIPProvider reports the state of the optional offline GeoIP database.
type GeoIPProvider interface {
	// GeoIPStatus returns database configuration/type/build date.
	GeoIPStatus() models.GeoIPStatus
}

// Deps are the read-only providers the snapshot is built from.
type Deps struct {
	Stream           *stream.Service
	WebRTC           WebRTCProvider
	Config           ConfigProvider
	GeoIP            GeoIPProvider
}

// Service builds live-status snapshots.
type Service struct {
	stream *stream.Service
	webrtc WebRTCProvider
	config ConfigProvider
	geoIP  GeoIPProvider
}

// New constructs the read-only live status service.
func New(deps Deps) *Service {
	return &Service{
		stream: deps.Stream,
		webrtc: deps.WebRTC,
		config: deps.Config,
		geoIP:  deps.GeoIP,
	}
}

// Status returns the current live-stream snapshot.
func (s *Service) Status() models.LiveStatus {
	status := models.LiveStatus{
		State:    models.LiveStateOffline,
		Viewers:  models.LiveViewerCounts{},
		Warnings: []models.StatusWarning{},
		FFmpeg: models.FFmpegStatus{
			ProcessModel: s.config.GetOutputProcessModel(),
			Processes:    []models.FFmpegProcessStatus{},
		},
		WebRTC: models.WebRTCStatus{
			Warnings: []models.StatusWarning{},
		},
		GeoIP: models.GeoIPStatus{},
	}

	publicStatus := s.stream.GetStatus()
	status.InputProtocol = publicStatus.InputProtocol

	ingestStatus := s.stream.GetIngestStatus()
	if ingestStatus != nil {
		startTime := ingestStatus.StartTime
		status.StartTime = &startTime
		status.UptimeSeconds = ingestStatus.UptimeSeconds
		status.StreamKeyID = ingestStatus.StreamKeyID
		status.Ingest = models.LiveIngestStatus{
			SessionID:     ingestStatus.SessionID,
			RemoteAddr:    ingestStatus.RemoteAddr,
			BitrateKbps:   ingestStatus.BitrateKbps,
			PacketLossPct: ingestStatus.PacketLossPct,
			RTTMs:         ingestStatus.RTTMs,
			Retransmitted: ingestStatus.Retransmitted,
			Dropped:       ingestStatus.DroppedPackets,
			Detail:        ingestStatus.Extra,
		}
	}
	if publicStatus.Online {
		status.State = models.LiveStateLive
	} else if ingestStatus != nil {
		// A publisher is connected but HLS is not being served yet.
		status.State = models.LiveStateWaiting
	}

	status.Source = sourceStatus(s.stream.GetBroadcaster())
	status.FFmpeg.Processes = s.stream.FFmpegProcesses()

	webrtcConfig := s.config.GetWebRTCConfig()
	webrtcViewers := 0
	if s.webrtc != nil {
		webrtcViewers = s.webrtc.ViewerCount()
	}
	hlsViewers := len(s.stream.GetActiveViewers())
	status.Viewers = models.LiveViewerCounts{
		Total:  hlsViewers + webrtcViewers,
		WebRTC: webrtcViewers,
		HLS:    hlsViewers,
	}

	webrtcWarnings := []models.StatusWarning{}
	if s.webrtc != nil {
		for index, warning := range s.webrtc.Warnings() {
			webrtcWarnings = append(webrtcWarnings, models.StatusWarning{
				Code:    fmt.Sprintf("webrtc_warning_%d", index),
				Message: warning,
			})
		}
	}
	status.WebRTC = models.WebRTCStatus{
		Enabled: webrtcConfig.Enabled,
		Capacity: models.WebRTCCapacity{
			Used:  webrtcViewers,
			Limit: webrtcConfig.MaxViewers,
		},
		Warnings: webrtcWarnings,
	}

	if s.geoIP != nil {
		status.GeoIP = s.geoIP.GeoIPStatus()
	}

	return status
}

// Viewers returns a filtered, sorted, paginated snapshot of live viewers.
func (s *Service) Viewers(page, pageSize int, sortBy, order, protocol, query string) models.LiveViewersPage {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 25
	}
	if pageSize > 200 {
		pageSize = 200
	}

	viewers := make([]models.LiveViewer, 0, 32)
	for _, viewer := range s.stream.GetActiveViewers() {
		row := models.LiveViewer{
			ClientID:        viewer.ClientID,
			IPAddress:       viewer.IPAddress,
			UserAgent:       viewer.UserAgent,
			Protocol:        models.LiveProtocolHLS,
			ConnectedAt:     viewer.FirstSeen,
			DurationSeconds: time.Since(viewer.FirstSeen).Seconds(),
			Source:          string(models.PlaybackSourceServer),
		}
		if viewer.Geo != nil {
			row.Country = viewer.Geo.CountryCode
			row.Region = viewer.Geo.RegionName
			row.LocationApprox = true
		}
		viewers = append(viewers, row)
	}
	if s.webrtc != nil {
		viewers = append(viewers, s.webrtc.LiveViewers()...)
	}

	if query != "" {
		needle := strings.ToLower(query)
		filtered := viewers[:0]
		for _, viewer := range viewers {
			if strings.Contains(strings.ToLower(viewer.IPAddress), needle) ||
				strings.Contains(strings.ToLower(viewer.ClientID), needle) ||
				strings.Contains(strings.ToLower(viewer.Country), needle) ||
				strings.Contains(strings.ToLower(viewer.City), needle) {
				filtered = append(filtered, viewer)
			}
		}
		viewers = filtered
	}
	if protocol != "" && protocol != "all" {
		filtered := viewers[:0]
		for _, viewer := range viewers {
			if viewer.Protocol == protocol {
				filtered = append(filtered, viewer)
			}
		}
		viewers = filtered
	}

	ascending := order == "asc"
	switch sortBy {
	case "duration":
		sort.SliceStable(viewers, func(a, b int) bool {
			if ascending {
				return viewers[a].DurationSeconds < viewers[b].DurationSeconds
			}
			return viewers[a].DurationSeconds > viewers[b].DurationSeconds
		})
	case "protocol":
		sort.SliceStable(viewers, func(a, b int) bool {
			if ascending {
				return viewers[a].Protocol < viewers[b].Protocol
			}
			return viewers[a].Protocol > viewers[b].Protocol
		})
	default: // connectedAt
		sort.SliceStable(viewers, func(a, b int) bool {
			if ascending {
				return viewers[a].ConnectedAt.Before(viewers[b].ConnectedAt)
			}
			return viewers[a].ConnectedAt.After(viewers[b].ConnectedAt)
		})
	}

	total := len(viewers)
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}

	return models.LiveViewersPage{
		Viewers:  viewers[start:end],
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}
}

func sourceStatus(broadcaster *models.Broadcaster) models.LiveSourceStatus {
	status := models.LiveSourceStatus{}
	if broadcaster == nil {
		return status
	}
	details := broadcaster.StreamDetails
	if details.Width > 0 {
		width := details.Width
		status.Width = &width
	}
	if details.Height > 0 {
		height := details.Height
		status.Height = &height
	}
	if details.VideoFramerate > 0 {
		fps := float64(details.VideoFramerate)
		status.FPS = &fps
	}
	status.VideoCodec = details.VideoCodec
	status.VideoProfile = details.VideoProfile
	status.VideoLevel = details.VideoLevel
	status.AudioCodec = details.AudioCodec
	status.HasBFrames = details.HasBFrames
	if details.VideoBitrate > 0 {
		bitrate := float64(details.VideoBitrate)
		status.VideoBitrateKbps = &bitrate
	}
	if details.AudioBitrate > 0 {
		bitrate := float64(details.AudioBitrate)
		status.AudioBitrateKbps = &bitrate
	}
	return status
}
