package stream

import (
	"time"

	"github.com/owncast/owncast/config"
	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/ingest"
)

// GetStatus returns a snapshot of the current stream state suitable for
// the public/admin status APIs.
func (s *Service) GetStatus() models.Status {
	if s.stats == nil {
		return models.Status{}
	}

	viewerCount := 0
	if s.IsStreamConnected() {
		viewerCount = len(s.stats.Viewers)
	}

	status := models.Status{
		Online:                s.IsStreamConnected(),
		ViewerCount:           viewerCount,
		OverallMaxViewerCount: s.stats.OverallMaxViewerCount,
		SessionMaxViewerCount: s.stats.SessionMaxViewerCount,
		LastDisconnectTime:    s.stats.LastDisconnectTime,
		LastConnectTime:       s.stats.LastConnectTime,
		VersionNumber:         config.VersionNumber,
		StreamTitle:           s.configRepository.GetStreamTitle(),
	}

	if session := s.GetCurrentSession(); session != nil {
		status.InputProtocol = string(session.Protocol())
	}

	return status
}

// GetCurrentBroadcast returns the in-flight broadcast settings, or nil
// if no stream is currently live.
func (s *Service) GetCurrentBroadcast() *models.CurrentBroadcast {
	return s.currentBroadcast
}

// GetCurrentSession returns the authenticated ingest session that started the
// current live stream, or nil when offline.
func (s *Service) GetCurrentSession() ingest.Session {
	s.sessionMu.RLock()
	defer s.sessionMu.RUnlock()
	return s.currentSession
}

// GetIngestStatus returns a protocol-neutral snapshot of the active ingest
// session. It returns nil while no publisher is connected. The stream key is
// represented by its non-secret identifier only.
func (s *Service) GetIngestStatus() *models.IngestStatus {
	session := s.GetCurrentSession()
	if session == nil {
		return nil
	}

	stats := session.Stats()
	age := session.Age()
	status := &models.IngestStatus{
		Protocol:       string(session.Protocol()),
		SessionID:      session.ID(),
		StreamKeyID:    session.StreamKeyID(),
		StartTime:      time.Now().Add(-age),
		UptimeSeconds:  age.Seconds(),
		BitrateKbps:    stats.BitrateKbps,
		PacketLossPct:  stats.PacketLossPct,
		RTTMs:          stats.RTTMs,
		Retransmitted:  stats.RetransmittedPackets,
		DroppedPackets: stats.DroppedPackets,
		Extra:          stats.Extra,
	}
	if addr := session.RemoteAddr(); addr != nil {
		status.RemoteAddr = addr.String()
	}
	status.Source = session.Broadcaster()
	return status
}

// FFmpegProcesses returns the supervised transcoder processes feeding the
// active stream. The list is empty until the transcoder supervisor is wired in
// Phase 4; the admin FFmpeg panel renders the empty state from that response.
func (s *Service) FFmpegProcesses() []models.FFmpegProcessStatus {
	return []models.FFmpegProcessStatus{}
}

// BroadcasterSet records the metadata of the inbound source. Implements
// StreamEvents; the local engine calls it from the RTMP metadata callback, a
// remote engine from its signaling channel.
func (s *Service) BroadcasterSet(broadcaster models.Broadcaster) {
	s.broadcaster = &broadcaster
	// Mirror the metadata onto the session so protocol-neutral consumers can
	// read it without depending on the legacy service field.
	if session := s.GetCurrentSession(); session != nil {
		session.SetBroadcaster(&broadcaster)
	}
}

// GetBroadcaster returns the active inbound broadcaster, or nil between
// streams.
func (s *Service) GetBroadcaster() *models.Broadcaster {
	if session := s.GetCurrentSession(); session != nil {
		if broadcaster := session.Broadcaster(); broadcaster != nil {
			return broadcaster
		}
	}
	return s.broadcaster
}
