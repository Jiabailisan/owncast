package stream

import (
	"fmt"
	"math"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/owncast/owncast/models"
)

// setupStats restores stats from the previous session and starts the
// periodic save + viewer-prune goroutines.
func (s *Service) setupStats() error {
	saved := s.getSavedStats()
	s.stats = &saved

	statsSaveTicker := time.NewTicker(1 * time.Minute)
	go func() {
		for range statsSaveTicker.C {
			s.saveStats()
		}
	}()

	viewerCountPruneTicker := time.NewTicker(5 * time.Second)
	go func() {
		for range viewerCountPruneTicker.C {
			s.pruneViewerCount()
		}
	}()

	return nil
}

// IsStreamConnected reports whether the stream is actively delivering
// live HLS content. Returns false during the brief warm-up after an
// RTMP connection where HLS segments aren't ready yet.
func (s *Service) IsStreamConnected() bool {
	if s.stats == nil || !s.stats.StreamConnected {
		return false
	}

	// Kind of a hack. It takes a handful of seconds between an RTMP
	// connection and when HLS data is available. Account for that with
	// an artificial buffer of a few segments.
	timeSinceLastConnected := time.Since(s.stats.LastConnectTime.Time).Seconds()
	waitTime := math.Max(float64(s.configRepository.GetStreamLatencyLevel().SecondsPerSegment)*3.0, 7)
	if timeSinceLastConnected < waitTime {
		return false
	}

	return s.stats.StreamConnected
}

// RemoveChatClient removes a chat client from the active-clients record.
// Currently unused — kept for now during the migration; remove once
// confirmed dead.
func (s *Service) RemoveChatClient(clientID string) {
	log.Trace("Removing the client:", clientID)

	s.statsMu.Lock()
	delete(s.stats.ChatClients, clientID)
	s.statsMu.Unlock()
}

// SetViewerActive marks a viewer as currently watching and updates the
// session/overall peak counters. Silent no-op while no stream is live.
func (s *Service) SetViewerActive(viewer *models.Viewer) {
	if s.stats == nil || !s.stats.StreamConnected {
		return
	}

	s.statsMu.Lock()
	defer s.statsMu.Unlock()

	// Asynchronously, optionally, fetch GeoIP info. The lookup can be slow,
	// so it lands on the stored viewer later, under the same lock every
	// other reader takes.
	go func(clientID, ipAddress string) {
		geo := s.geoIPClient.GetGeoFromIP(ipAddress)

		s.statsMu.Lock()
		defer s.statsMu.Unlock()
		if stored, ok := s.stats.Viewers[clientID]; ok {
			stored.Geo = geo
		}
	}(viewer.ClientID, viewer.IPAddress)

	if stored, exists := s.stats.Viewers[viewer.ClientID]; exists {
		stored.LastSeen = time.Now()
		mergeViewerQuality(stored, viewer)
	} else {
		s.stats.Viewers[viewer.ClientID] = viewer
	}
	s.stats.SessionMaxViewerCount = int(math.Max(float64(len(s.stats.Viewers)), float64(s.stats.SessionMaxViewerCount)))
	s.stats.OverallMaxViewerCount = int(math.Max(float64(s.stats.SessionMaxViewerCount), float64(s.stats.OverallMaxViewerCount)))
}

// SetViewerActiveWithVariant marks a viewer as active and records the
// playback quality of the HLS variant it is currently consuming.
// variantIndex is the numeric variant directory of the requested HLS path
// (/hls/<index>/...); the master playlist carries no variant. An out-of-range
// or non-HLS variant index still records the viewer as active but leaves the
// quality fields untouched: quality is never invented.
func (s *Service) SetViewerActiveWithVariant(viewer *models.Viewer, variantIndex int) {
	if s.stats == nil || !s.stats.StreamConnected {
		return
	}

	s.applyViewerQuality(viewer, variantIndex)
	s.SetViewerActive(viewer)
}

// applyViewerQuality fills a request's viewer record with the advertised
// quality of the active output variant at variantIndex.
func (s *Service) applyViewerQuality(viewer *models.Viewer, variantIndex int) {
	variants := s.activeOutputVariants()
	if variantIndex < 0 || variantIndex >= len(variants) {
		return
	}

	variant := variants[variantIndex]
	if !variant.HLSEnabled() {
		return
	}

	index := variantIndex
	viewer.VariantIndex = &index
	viewer.VariantName = variant.GetName()
	viewer.BitrateKbps, viewer.Resolution, viewer.FPS = hlsVariantPlaybackQuality(variant, s.sourceStreamDetails())
}

// activeOutputVariants returns the output settings of the current broadcast,
// falling back to the persisted configuration when no broadcast snapshot is
// held (offline, tests).
func (s *Service) activeOutputVariants() []models.StreamOutputVariant {
	if s.currentBroadcast != nil && len(s.currentBroadcast.OutputSettings) > 0 {
		return s.currentBroadcast.OutputSettings
	}
	if s.configRepository == nil {
		return nil
	}

	return s.configRepository.GetStreamOutputVariants()
}

// sourceStreamDetails returns the ingress source properties, or nil when no
// broadcaster metadata has arrived yet.
func (s *Service) sourceStreamDetails() *models.InboundStreamDetails {
	broadcaster := s.GetBroadcaster()
	if broadcaster == nil {
		return nil
	}

	details := broadcaster.StreamDetails

	return &details
}

// mergeViewerQuality copies newly observed playback quality onto an existing
// viewer record. A request that carries no variant information (the ping
// endpoint, a master-playlist request) must not erase quality observed from an
// earlier variant playlist request.
func mergeViewerQuality(stored, observed *models.Viewer) {
	if observed.VariantIndex == nil {
		return
	}

	stored.VariantIndex = observed.VariantIndex
	stored.VariantName = observed.VariantName
	stored.BitrateKbps = observed.BitrateKbps
	stored.Resolution = observed.Resolution
	stored.FPS = observed.FPS
}

// hlsVariantPlaybackQuality derives the quality an HLS viewer receives from
// one output variant. copyVariant variants (video passthrough) carry no
// encoder settings, so their values come from the ingress source when it is
// known. Unknown source properties stay nil/empty instead of being guessed.
func hlsVariantPlaybackQuality(variant models.StreamOutputVariant, source *models.InboundStreamDetails) (*float64, string, *float64) {
	copyVariant := variant.IsVideoPassthrough ||
		(variant.HLSOutput() != nil && strings.EqualFold(variant.HLSOutput().Mode, models.OutputModeCopy))

	return hlsVariantBitrate(variant, source, copyVariant),
		hlsVariantResolution(variant, source, copyVariant),
		hlsVariantFPS(variant, source, copyVariant)
}

// hlsVariantBitrate returns the total (video+audio) bitrate of the variant.
// The video bitrate must be known for the total to be meaningful; a known
// audio bitrate is added when available.
func hlsVariantBitrate(variant models.StreamOutputVariant, source *models.InboundStreamDetails, copyVariant bool) *float64 {
	videoBitrateKbps := 0
	audioBitrateKbps := 0

	if copyVariant {
		if source != nil {
			videoBitrateKbps = source.VideoBitrate
			audioBitrateKbps = source.AudioBitrate
		}
	} else {
		videoBitrateKbps = variant.VideoBitrate
		if variant.GetIsAudioPassthrough() {
			if source != nil {
				audioBitrateKbps = source.AudioBitrate
			}
		} else {
			audioBitrateKbps = variant.AudioBitrate
		}
	}

	if videoBitrateKbps <= 0 {
		return nil
	}
	if audioBitrateKbps < 0 {
		audioBitrateKbps = 0
	}

	total := float64(videoBitrateKbps + audioBitrateKbps)

	return &total
}

// hlsVariantResolution returns "WxH" for the variant's output resolution.
// A copy variant keeps the source resolution; a scaled transcode variant uses
// its configured dimensions (deriving the unset dimension from the source
// aspect ratio like FFmpeg's -2); an unscaled transcode keeps the source
// resolution. Unknown source geometry yields an empty string.
func hlsVariantResolution(variant models.StreamOutputVariant, source *models.InboundStreamDetails, copyVariant bool) string {
	if copyVariant {
		return sourceResolution(source)
	}

	width := variant.ScaledWidth
	height := variant.ScaledHeight
	switch {
	case width > 0 && height > 0:
		return fmt.Sprintf("%dx%d", width, height)
	case width == 0 && height == 0:
		return sourceResolution(source)
	case source == nil || source.Width <= 0 || source.Height <= 0:
		return ""
	case width > 0:
		derivedHeight := evenRound(float64(width) * float64(source.Height) / float64(source.Width))
		if derivedHeight > 0 {
			return fmt.Sprintf("%dx%d", width, derivedHeight)
		}
	case height > 0:
		derivedWidth := evenRound(float64(height) * float64(source.Width) / float64(source.Height))
		if derivedWidth > 0 {
			return fmt.Sprintf("%dx%d", derivedWidth, height)
		}
	}

	return ""
}

// hlsVariantFPS returns the frame rate the variant is encoded at. Copy
// variants carry the source frame rate; transcoded variants use their
// configured frame rate (the encoder default when unset).
func hlsVariantFPS(variant models.StreamOutputVariant, source *models.InboundStreamDetails, copyVariant bool) *float64 {
	if copyVariant {
		return sourceFPS(source)
	}

	fps := float64(variant.GetFramerate())
	if fps <= 0 {
		return sourceFPS(source)
	}

	return &fps
}

// sourceResolution formats the ingress source resolution, or returns "" when
// it is unknown.
func sourceResolution(source *models.InboundStreamDetails) string {
	if source == nil || source.Width <= 0 || source.Height <= 0 {
		return ""
	}

	return fmt.Sprintf("%dx%d", source.Width, source.Height)
}

// sourceFPS returns the ingress source frame rate, or nil when unknown.
func sourceFPS(source *models.InboundStreamDetails) *float64 {
	if source == nil || source.VideoFramerate <= 0 {
		return nil
	}

	fps := float64(source.VideoFramerate)

	return &fps
}

// evenRound rounds a derived dimension to the nearest even number, matching
// the even-dimension rounding of FFmpeg's aspect-ratio preserving scaling.
func evenRound(value float64) int {
	return int(math.Round(value/2)) * 2
}

// GetActiveViewers returns a snapshot of the currently-tracked viewers.
// The viewers are copied because the live map and the viewers in it are
// mutated by request handlers and the prune goroutine while callers read.
func (s *Service) GetActiveViewers() map[string]models.Viewer {
	if s.stats == nil {
		return nil
	}

	s.statsMu.Lock()
	defer s.statsMu.Unlock()

	viewers := make(map[string]models.Viewer, len(s.stats.Viewers))
	for id, viewer := range s.stats.Viewers {
		viewers[id] = *viewer
	}
	return viewers
}

func (s *Service) pruneViewerCount() {
	viewers := make(map[string]*models.Viewer)

	s.statsMu.Lock()
	defer s.statsMu.Unlock()

	for viewerID, viewer := range s.stats.Viewers {
		if time.Since(s.stats.Viewers[viewerID].LastSeen) < activeViewerPurgeTimeout {
			viewers[viewerID] = viewer
		}
	}

	s.stats.Viewers = viewers
}

func (s *Service) saveStats() {
	if err := s.configRepository.SetPeakOverallViewerCount(s.stats.OverallMaxViewerCount); err != nil {
		log.Errorln("error saving viewer count", err)
	}
	if err := s.configRepository.SetPeakSessionViewerCount(s.stats.SessionMaxViewerCount); err != nil {
		log.Errorln("error saving viewer count", err)
	}
	// Persist a recent live heartbeat while the stream is online so an unclean
	// shutdown (SIGKILL, node reboot, container eviction) still leaves a useful
	// "last live" timestamp on the next startup. A graceful disconnect below will
	// overwrite this with the real end time.
	if s.stats.StreamConnected && s.stats.LastConnectTime != nil && s.stats.LastConnectTime.Valid {
		if err := s.configRepository.SetLastDisconnectTime(time.Now()); err != nil {
			log.Errorln("error saving live heartbeat time", err)
		}
		return
	}
	if s.stats.LastDisconnectTime != nil && s.stats.LastDisconnectTime.Valid {
		if err := s.configRepository.SetLastDisconnectTime(s.stats.LastDisconnectTime.Time); err != nil {
			log.Errorln("error saving disconnect time", err)
		}
	}
}

func (s *Service) getSavedStats() models.Stats {
	savedLastDisconnectTime, _ := s.configRepository.GetLastDisconnectTime()

	result := models.Stats{
		ChatClients:           make(map[string]models.Client),
		Viewers:               make(map[string]*models.Viewer),
		SessionMaxViewerCount: s.configRepository.GetPeakSessionViewerCount(),
		OverallMaxViewerCount: s.configRepository.GetPeakOverallViewerCount(),
		LastDisconnectTime:    savedLastDisconnectTime,
	}

	// If the stats were saved > 5min ago then ignore the
	// peak session count value, since the session is over.
	if result.LastDisconnectTime == nil || !result.LastDisconnectTime.Valid || time.Since(result.LastDisconnectTime.Time).Minutes() > 5 {
		result.SessionMaxViewerCount = 0
	}

	return result
}
