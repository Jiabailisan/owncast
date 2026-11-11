// Package livestatus aggregates a read-only point-in-time snapshot of the live
// stream for the admin Live Stream Status API (spec section 6). It has no
// lifecycle of its own and holds no state: every call reads the current state
// from the injected providers.
package livestatus

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/geoip"
	"github.com/owncast/owncast/services/stream"
)

// ConfigProvider is the narrow read-only configuration surface needed here.
type ConfigProvider interface {
	GetWebRTCConfig() models.WebRTCConfig
	GetOutputProcessModel() string
	// GetVideoCodec returns the configured H.264 encoder name (libx264,
	// h264_nvenc, ...) used for outbound-stream details.
	GetVideoCodec() string
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

// GeoIPProvider reports the state of the optional offline GeoIP database and
// resolves viewer locations.
type GeoIPProvider interface {
	// GeoIPStatus returns database configuration/type/build date.
	GeoIPStatus() models.GeoIPStatus
	// GeoDetails returns the location of an IP address, or nil when unknown.
	GeoDetails(ip string) *geoip.GeoDetails
}

// Deps are the read-only providers the snapshot is built from.
type Deps struct {
	Stream *stream.Service
	WebRTC WebRTCProvider
	Config ConfigProvider
	GeoIP  GeoIPProvider
	// Names resolves the display name of the chat user watching from an address.
	// Chat names change (the operator can rename a user), so the viewer table reads
	// the current one instead of trusting whatever the player reported earlier.
	Names NameProvider
}

// NameProvider resolves the display name of the chat user a player belongs to.
// Names are mutable, so it is consulted when the table is built.
type NameProvider interface {
	DisplayNameForToken(accessToken string) string
	// DisplayNamesForTokens resolves a whole page in one pass, so the viewer table
	// does not take the chat lock once per row.
	DisplayNamesForTokens(accessTokens []string) map[string]string
}

// Service builds live-status snapshots.
type Service struct {
	stream *stream.Service
	webrtc WebRTCProvider
	config ConfigProvider
	geoIP  GeoIPProvider
	names  NameProvider
}

// New constructs the read-only live status service.
func New(deps Deps) *Service {
	return &Service{
		stream: deps.Stream,
		webrtc: deps.WebRTC,
		config: deps.Config,
		geoIP:  deps.GeoIP,
		names:  deps.Names,
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
	// The native viewer map already contains WebRTC viewers: the browser pings
	// /api/ping for both transports. Counting every entry as HLS and then adding
	// the WebRTC sessions on top reported each WebRTC viewer twice, so the cards
	// disagreed with the table.
	hlsViewers := 0
	for _, viewer := range s.stream.GetActiveViewers() {
		if !strings.EqualFold(viewer.Protocol, "webrtc") {
			hlsViewers++
		}
	}
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

	status.GeoIP = geoIPStatusFromProvider(s.geoIP)
	status.Outbound = outboundStatus(s.stream.GetCurrentBroadcast(), s.stream.GetBroadcaster(), s.config.GetVideoCodec(), s.config.GetWebRTCConfig().Enabled)
	status.Warnings = append(status.Warnings, geoIPWarnings(status.GeoIP)...)
	// WebRTC leg-planning warnings: copy fallbacks, process restarts and
	// capacity/domain notes surfaced by the stream lifecycle.
	status.Warnings = append(status.Warnings, s.stream.WebRTCWarnings()...)

	return status
}

// Viewers returns a filtered, sorted, paginated snapshot of live viewers.
func (s *Service) Viewers(page, pageSize int, sortBy, order, protocol, query string) models.LiveViewersPage {
	page, pageSize = normalizeViewerPage(page, pageSize)
	viewers := s.collectLiveViewers()
	viewers = filterLiveViewers(viewers, protocol, query)
	sortLiveViewers(viewers, sortBy, order == "asc")
	return paginateLiveViewers(viewers, page, pageSize)
}

// normalizeViewerPage clamps the pagination parameters to the API bounds.
// maxViewerPage bounds the page number. page and pageSize both come from the query
// string, and (page-1)*pageSize overflowed for a large page, which turned the
// slice bounds negative and panicked the handler.
const maxViewerPage = 1_000_000

func normalizeViewerPage(page, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}

	if page > maxViewerPage {
		page = maxViewerPage
	}
	if pageSize < 1 {
		pageSize = 25
	}
	if pageSize > 200 {
		pageSize = 200
	}
	return page, pageSize
}

// collectLiveViewers combines the in-memory HLS viewers (with their GeoIP
// details) and the WebRTC viewer rows from the delivery service.
func (s *Service) collectLiveViewers() []models.LiveViewer {
	viewers := make([]models.LiveViewer, 0, 32)

	if s.webrtc != nil {
		// WebRTC delivery knows the viewer address but not the location, so fill
		// the geo fields here; without this every WebRTC viewer shows N/A.
		for _, row := range s.webrtc.LiveViewers() {
			s.applyViewerLocation(&row)
			viewers = append(viewers, row)
		}
	}

	// A WebRTC session row carries no client id of its own, so player reports are
	// matched to it by address. They are held in a queue and consumed one per row:
	// matching by address and stopping at the first hit gave every report to the
	// first viewport behind a NAT and left the others with no data at all.
	pendingWebRTCReports := map[string][]models.Viewer{}
	tokens := map[string]string{}

	if s.stream != nil {
		for _, viewer := range s.stream.GetActiveViewers() {
			if strings.EqualFold(viewer.Protocol, "webrtc") {
				pendingWebRTCReports[viewer.IPAddress] = append(pendingWebRTCReports[viewer.IPAddress], viewer)
				if viewer.ReportedToken != "" {
					tokens["webrtc:"+viewer.IPAddress] = viewer.ReportedToken
				}

				continue
			}

			// HLS viewers keep their own identity, so two people behind one address
			// stay two rows with their own names.
			if viewer.ReportedToken != "" {
				tokens["client:"+viewer.ClientID] = viewer.ReportedToken
			}

			viewers = append(viewers, liveViewerFromViewer(viewer))
		}
	}

	distributeWebRTCReports(viewers, pendingWebRTCReports)

	s.applyDisplayNames(viewers, tokens)

	return viewers
}

// applyDisplayNames replaces the reported name with the current chat name of the
// user the player belongs to. A rename must show up immediately, and a chat-only
// client on the same address must never be mistaken for the person watching.
func (s *Service) applyDisplayNames(viewers []models.LiveViewer, tokens map[string]string) {
	if s.names == nil {
		return
	}

	// One lookup for the whole page instead of one per row: the table refreshes
	// every ten seconds and each call takes the chat lock.
	wanted := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if token != "" {
			wanted = append(wanted, token)
		}
	}

	if len(wanted) == 0 {
		return
	}

	resolved := s.names.DisplayNamesForTokens(wanted)

	for index := range viewers {
		var token string

		// A WebRTC row has no client id, so its name is looked up by address; an HLS row
		// must only use its own identity, otherwise it inherits the name of whoever is
		// watching over WebRTC from the same address.
		if strings.EqualFold(viewers[index].Protocol, "webrtc") {
			token = tokens["webrtc:"+viewers[index].IPAddress]
		} else {
			token = tokens["client:"+viewers[index].ClientID]
		}

		if name := resolved[token]; name != "" {
			viewers[index].Username = name
		}
	}
}

// distributeWebRTCReports hands one player report to each WebRTC row at an
// address. Consuming the queue keeps two sessions behind one NAT from both
// reading the same report and stamps each row with its own data.
func distributeWebRTCReports(rows []models.LiveViewer, reports map[string][]models.Viewer) {
	for index := range rows {
		if !strings.EqualFold(rows[index].Protocol, "webrtc") {
			continue
		}

		queue := reports[rows[index].IPAddress]
		if len(queue) == 0 {
			continue
		}

		mergeReportedIntoRow(&rows[index], queue[0])
		reports[rows[index].IPAddress] = queue[1:]
	}
}

// mergeReportedIntoRow copies player-reported details onto one WebRTC row, so
// one person stays one row with everything they reported.
func mergeReportedIntoRow(row *models.LiveViewer, viewer models.Viewer) {
	if viewer.DisplayName != "" {
		row.Username = viewer.DisplayName
	}
	if viewer.ReportedQuality != "" {
		row.Quality = viewer.ReportedQuality
	}
	// Keep the rendition the session already reported; only fall back to the
	// player's variant when it has nothing better.
	if (row.Quality == "" || strings.EqualFold(row.Quality, "auto")) && viewer.VariantName != "" {
		row.Quality = viewer.VariantName
	}
	if viewer.ReportedLatency != nil {
		row.LatencyMs = viewer.ReportedLatency
	}
}

// filterLiveViewers applies the optional protocol and free-text filters.
func filterLiveViewers(viewers []models.LiveViewer, protocol, query string) []models.LiveViewer {
	if query == "" && (protocol == "" || protocol == "all") {
		return viewers
	}

	needle := strings.ToLower(query)
	filtered := viewers[:0]
	for _, viewer := range viewers {
		if query != "" &&
			!strings.Contains(strings.ToLower(viewer.IPAddress), needle) &&
			!strings.Contains(strings.ToLower(viewer.ClientID), needle) &&
			!strings.Contains(strings.ToLower(viewer.Country), needle) &&
			!strings.Contains(strings.ToLower(viewer.Region), needle) &&
			!strings.Contains(strings.ToLower(viewer.City), needle) {
			continue
		}
		if protocol != "" && protocol != "all" && viewer.Protocol != protocol {
			continue
		}
		filtered = append(filtered, viewer)
	}
	return filtered
}

// sortLiveViewers sorts the rows in place by the requested column.
func sortLiveViewers(viewers []models.LiveViewer, sortBy string, ascending bool) {
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
	case "bitrate":
		// The table offers a sorter on this column; without a branch it fell through
		// to connection time, so clicking the header changed nothing visible. Rows
		// without a measurement sort last in both directions.
		sort.SliceStable(viewers, func(a, b int) bool {
			left, right := viewers[a].BitrateKbps, viewers[b].BitrateKbps

			switch {
			case left == nil && right == nil:
				return false
			case left == nil:
				return false
			case right == nil:
				return true
			case ascending:
				return *left < *right
			default:
				return *left > *right
			}
		})
	default: // connectedAt
		sort.SliceStable(viewers, func(a, b int) bool {
			if ascending {
				return viewers[a].ConnectedAt.Before(viewers[b].ConnectedAt)
			}
			return viewers[a].ConnectedAt.After(viewers[b].ConnectedAt)
		})
	}
}

// paginateLiveViewers returns the requested page of the viewer rows.
func paginateLiveViewers(viewers []models.LiveViewer, page, pageSize int) models.LiveViewersPage {
	total := len(viewers)

	// Clamp in 64-bit space: both arguments are caller supplied, so the
	// multiplication can overflow and produce negative bounds.
	start64 := (int64(page) - 1) * int64(pageSize)
	if start64 < 0 {
		start64 = 0
	}

	if start64 > int64(total) {
		start64 = int64(total)
	}

	// Narrow only after clamping, so a huge page cannot overflow the conversion.
	start := int(start64)
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

// liveViewerFromViewer maps one in-memory HLS viewer, including its optional
// GeoIP details, to the admin LiveViewer row. City/region values are
// approximate and must never be used for access control.
// applyViewerLocation fills the geo fields of a viewer row that has none.
func (s *Service) applyViewerLocation(row *models.LiveViewer) {
	if row == nil || s.geoIP == nil {
		return
	}
	if row.Country != "" || row.Region != "" || row.City != "" {
		return
	}
	details := s.geoIP.GeoDetails(row.IPAddress)
	if details == nil {
		return
	}
	row.Country = details.CountryCode
	row.Region = details.RegionName
	row.City = details.City
	row.LocationApprox = true
}

func liveViewerFromViewer(viewer models.Viewer) models.LiveViewer {
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
		row.City = viewer.Geo.City
		row.LocationApprox = true
	}
	// HLS playback quality observed from the variant playlist the viewer is
	// currently requesting. Nil/empty until a variant has been observed.
	row.BitrateKbps = viewer.BitrateKbps
	row.Resolution = viewer.Resolution
	row.FPS = viewer.FPS

	// Player-reported details: the server cannot measure latency or the display
	// name, and the built-in players report both with their heartbeat.
	row.Username = viewer.DisplayName
	row.Quality = viewer.ReportedQuality
	if row.Quality == "" || strings.EqualFold(row.Quality, "auto") {
		switch {
		case viewer.VariantName != "":
			row.Quality = "Auto (" + viewer.VariantName + ")"
		case row.Resolution != "":
			// The variant name is unknown but the server measured the resolution
			// it is delivering, which is enough to tell renditions apart.
			row.Quality = "Auto (" + row.Resolution + ")"
		}
	}
	// A player that reports the variant index instead of its name still gets a
	// readable quality: the server knows the variant it is serving.
	if viewer.VariantName != "" {
		if _, err := strconv.Atoi(row.Quality); err == nil {
			row.Quality = viewer.VariantName
		}
	}
	if row.Quality == "" {
		row.Quality = viewer.VariantName
	}
	row.LatencyMs = viewer.ReportedLatency

	return row
}

// geoIPStatusFromProvider reads the optional offline GeoIP database status.
func geoIPStatusFromProvider(provider GeoIPProvider) models.GeoIPStatus {
	if provider == nil {
		return models.GeoIPStatus{}
	}
	return provider.GeoIPStatus()
}

// geoIPWarnings reports the missing-database condition as an operator-visible
// status warning (spec 6.2 lists "GeoIP DB missing" as a warning).
func geoIPWarnings(status models.GeoIPStatus) []models.StatusWarning {
	if status.Configured {
		return nil
	}
	return []models.StatusWarning{{
		Code:    "geoip_database_missing",
		Message: "GeoIP database not configured; viewer location fields will show N/A. Supply a MaxMind GeoLite2 City .mmdb at data/GeoLite2-City.mmdb.",
	}}
}

// GeoIPStatusFromDatabaseMetadata converts the geoip package metadata into the
// admin Live Stream Status model, including the additive per-database sources
// list (primary first). It lives here so main.go's provider adapter stays a
// one-liner and the conversion is unit-testable.
func GeoIPStatusFromDatabaseMetadata(metadata geoip.DatabaseMetadata) models.GeoIPStatus {
	status := models.GeoIPStatus{Configured: metadata.Configured}
	if metadata.Configured {
		status.DatabaseType = metadata.DatabaseType
		status.BuildDate = metadata.BuildDate
	}
	for _, source := range metadata.Sources {
		status.Sources = append(status.Sources, models.GeoIPDatabaseSource{
			Role:         source.Role,
			Configured:   source.Configured,
			Path:         source.Path,
			DatabaseType: source.DatabaseType,
			BuildDate:    source.BuildDate,
			HasCity:      source.HasCity,
			Error:        source.Error,
		})
	}
	return status
}

// outboundStatus maps the active stream plan (the current broadcast's output
// variants) to the additive Outbound Stream Details model. Passthrough streams
// use the inbound source bitrate when known; unknown values stay nil (N/A) and
// are never estimated as zero.
func outboundStatus(broadcast *models.CurrentBroadcast, broadcaster *models.Broadcaster, videoCodec string, webRTCEnabled bool) models.LiveOutboundStatus {
	status := models.LiveOutboundStatus{Streams: []models.LiveOutboundStream{}}
	if broadcast == nil || len(broadcast.OutputSettings) == 0 {
		return status
	}

	source := models.InboundStreamDetails{}
	if broadcaster != nil {
		source = broadcaster.StreamDetails
	}

	explicitWebRTC := false
	for _, variant := range broadcast.OutputSettings {
		if variant.WebRTCEnabled() {
			explicitWebRTC = true
			break
		}
	}

	for index, variant := range broadcast.OutputSettings {
		stream := models.LiveOutboundStream{
			VariantIndex: index,
			Name:         variant.GetName(),
			Protocols:    outboundProtocols(variant, explicitWebRTC, webRTCEnabled),
		}

		if variant.IsVideoPassthrough {
			stream.VideoEncoder = "copy"
			stream.VideoBitrateKbps = positiveBitrate(source.VideoBitrate)
		} else {
			stream.VideoEncoder = outboundVideoEncoder(videoCodec)
			stream.VideoBitrateKbps = positiveBitrate(variant.VideoBitrate)
		}

		if variant.GetIsAudioPassthrough() {
			stream.AudioEncoder = "copy"
			stream.AudioBitrateKbps = positiveBitrate(source.AudioBitrate)
		} else {
			stream.AudioEncoder = "aac"
			stream.AudioBitrateKbps = positiveBitrate(variant.AudioBitrate)
		}

		status.Streams = append(status.Streams, stream)
	}
	return status
}

// outboundProtocols reports which delivery protocols this output variant is
// planned for. The implicit WebRTC rule matches the stream lifecycle: when no
// variant has an explicit WebRTC mapping, every HLS variant is also delivered
// over WebRTC.
func outboundProtocols(variant models.StreamOutputVariant, explicitWebRTC, webRTCEnabled bool) []string {
	protocols := make([]string, 0, 2)
	if variant.HLSEnabled() {
		protocols = append(protocols, "hls")
	}
	webRTC := webRTCEnabled && (variant.WebRTCEnabled() || (!explicitWebRTC && variant.HLSEnabled()))
	if webRTC {
		protocols = append(protocols, "webrtc")
	}
	return protocols
}

// outboundVideoEncoder converts the configured codec value into the outbound
// encoder label ("copy" is handled by the caller for passthrough variants).
func outboundVideoEncoder(codec string) string {
	switch strings.ToLower(strings.TrimSpace(codec)) {
	case "", "libx264", "x264":
		return "libx264"
	default:
		return codec
	}
}

// positiveBitrate returns a pointer to value when it is a usable bitrate, or
// nil (N/A) when the plan/source does not provide one.
func positiveBitrate(value int) *float64 {
	if value <= 0 {
		return nil
	}
	bitrate := float64(value)
	return &bitrate
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

	// Additive SRT audio codec details. RTMP leaves these zero, so the keys
	// stay omitted (omitempty) and the existing payload is unchanged.
	status.AudioProfile = details.AudioProfile
	if details.AudioSampleRate > 0 {
		sampleRate := details.AudioSampleRate
		status.AudioSampleRate = &sampleRate
	}
	if details.AudioChannels > 0 {
		channels := details.AudioChannels
		status.AudioChannels = &channels
	}
	return status
}
