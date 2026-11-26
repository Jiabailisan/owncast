package stream

import (
	"time"

	"github.com/owncast/owncast/config"
	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/ingest"
)

// publicPipelineHLS is the public delivery pipeline label exposed on the
// status API (the HLS output is always available alongside WebRTC).
const publicPipelineHLS = "hls"

// GetStatus returns a snapshot of the current stream state suitable for
// the public/admin status APIs.
// GetRunningProcessModel returns the FFmpeg process model the active stream was
// started with, or an empty string when no stream is running.
func (s *Service) GetRunningProcessModel() string {
	s.processModelMu.RLock()
	defer s.processModelMu.RUnlock()

	return s.runningProcessModel
}

func (s *Service) GetStatus() models.Status {
	if s.stats == nil {
		return models.Status{}
	}

	// Resolve connectivity before taking statsMu: IsStreamConnected takes the same
	// read lock, and a recursive read lock deadlocks as soon as a writer queues.
	online := s.IsStreamConnected()

	// The viewer map and the counters are mutated by the metrics ticker and by
	// every playlist request, so copy all fields under the lock.
	s.statsMu.RLock()
	viewerCount := 0
	if online {
		viewerCount = len(s.stats.Viewers)
	}
	overallMax := s.stats.OverallMaxViewerCount
	sessionMax := s.stats.SessionMaxViewerCount
	lastDisconnect := s.stats.LastDisconnectTime
	lastConnect := s.stats.LastConnectTime
	s.statsMu.RUnlock()

	status := models.Status{
		Online:                online,
		ViewerCount:           viewerCount,
		OverallMaxViewerCount: overallMax,
		SessionMaxViewerCount: sessionMax,
		LastDisconnectTime:    lastDisconnect,
		LastConnectTime:       lastConnect,
		VersionNumber:         config.VersionNumber,
		StreamTitle:           s.currentStreamTitle(),
	}

	if session := s.GetCurrentSession(); session != nil {
		status.InputProtocol = string(session.Protocol())
	}

	// Additive viewer metering: expose the active source bitrates and the
	// public delivery pipeline so the browser Stats for nerds panel can show
	// the ingest audio bitrate (which it cannot measure itself) and label the
	// live mode.
	if broadcaster := s.GetBroadcaster(); broadcaster != nil {
		status.VideoBitrate = broadcaster.StreamDetails.VideoBitrate
		status.AudioBitrate = broadcaster.StreamDetails.AudioBitrate
	}
	if status.Online {
		status.VideoPipeline = publicPipelineHLS
	}

	return status
}

// currentStreamTitle returns the title the live publisher asked for, or the saved default
// when it did not ask for one. Every consumer that shows a title reads this one value, so a
// per-stream title cannot appear in one place and not another.
func (s *Service) currentStreamTitle() string {
	if session := s.GetCurrentSession(); session != nil {
		if title := session.StreamTitle(); title != "" {
			return title
		}
	}

	return s.configRepository.GetStreamTitle()
}

// SaveDefaultTitle persists a new default title and reports whether the title viewers see
// actually changes. A live publisher may set its own title from its stream key, and that
// title overrides the default, so saving a new default while one is set changes nothing for
// viewers: the save still succeeds and the admin field still shows the new default, but
// there is nothing to announce.
func (s *Service) SaveDefaultTitle(save func(string) error, value string) (bool, error) {
	// The comparison is only meaningful within one stream. A publish may start or end while the
	// default is being written, and comparing titles across two sessions would report a change
	// that viewers never saw, so the session identity is sampled on both sides of the save.
	sessionBefore, titleBefore := s.effectiveTitleWithSession()

	if err := save(value); err != nil {
		return false, err
	}

	sessionAfter, titleAfter := s.effectiveTitleWithSession()

	if sessionAfter != sessionBefore {
		// A different session (started, ended or replaced) means this save spans two streams.
		// It still succeeded; there is simply nothing to announce.
		return false, nil
	}

	if sessionAfter != nil && sessionAfter.StreamTitle() != "" {
		// A stable publisher with its own title overrides the default, so viewers keep seeing it.
		// This has to read the session's own title: titleAfter is what viewers see, which is the
		// freshly saved default when the publisher did not set one.
		return false, nil
	}

	return value != "" && titleAfter != titleBefore, nil
}

// effectiveTitleWithSession returns the title viewers see right now together with the session it
// came from. The session is nil when the default applies, and it is what tells two reads apart:
// the same pointer means the same stream.
func (s *Service) effectiveTitleWithSession() (ingest.Session, string) {
	s.sessionMu.RLock()
	session := s.currentSession
	s.sessionMu.RUnlock()

	if session != nil {
		if title := session.StreamTitle(); title != "" {
			return session, title
		}
	}

	return session, s.configRepository.GetStreamTitle()
}

// GetCurrentBroadcast returns the in-flight broadcast settings, or nil
// if no stream is currently live.
func (s *Service) GetCurrentBroadcast() *models.CurrentBroadcast {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()

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

// BroadcasterSet records the metadata of the inbound source. Implements
// StreamEvents; the local engine calls it from the RTMP metadata callback, a
// remote engine from its signaling channel.
func (s *Service) BroadcasterSet(broadcaster models.Broadcaster) {
	// The metadata belongs to the broadcast that is live when it arrives; the epoch is
	// stamped under the same lock that publishes the broadcast so a reader can tell
	// whether it still describes the stream it is looking at.
	s.statsMu.Lock()
	s.broadcaster = &broadcaster
	s.broadcasterEpoch = s.broadcastEpoch
	s.statsMu.Unlock()

	// Mirror the metadata onto the session so protocol-neutral consumers can
	// read it without depending on the legacy service field.
	if session := s.GetCurrentSession(); session != nil {
		session.SetBroadcaster(&broadcaster)
	}
}

// BroadcasterSetFromSession records metadata that a local listener produced for one
// session. The session is the source of truth: if it already carries the metadata, that
// copy is used rather than a second one, and the service-level state is only updated
// while the session still owns the broadcast, so a callback that arrives after a newer
// stream took over changes nothing.
func (s *Service) BroadcasterSetFromSession(session ingest.Session, broadcaster models.Broadcaster) {
	if session != nil {
		if own := session.Broadcaster(); own != nil {
			broadcaster = *own
		}
	}

	// The ownership check and the write share one critical section and compare against
	// the session the live broadcast was published with, not against a separately read
	// current session: an old session, or one whose broadcast has ended, is refused.
	s.statsMu.Lock()
	defer s.statsMu.Unlock()

	if session != nil && s.broadcastSession != session {
		return
	}

	s.broadcaster = &broadcaster
	s.broadcasterEpoch = s.broadcastEpoch
}

// GetBroadcaster returns the active inbound broadcaster, or nil between
// streams.
func (s *Service) GetBroadcaster() *models.Broadcaster {
	// A session carries its own metadata (SRT), so prefer it: it cannot be confused
	// with a later broadcast.
	if session := s.GetCurrentSession(); session != nil {
		if broadcaster := session.Broadcaster(); broadcaster != nil {
			return broadcaster
		}
	}

	s.statsMu.Lock()
	defer s.statsMu.Unlock()

	return s.broadcaster
}

// broadcasterForEpoch returns the service-level metadata only while it still belongs to
// the given broadcast. A caller that works from a snapshot uses this so the metadata and
// the snapshot describe the same stream.
func (s *Service) broadcasterForEpoch(epoch uint64) *models.Broadcaster {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()

	// The stamp must match the requested broadcast and that broadcast must still be the
	// current one: metadata left over from a finished stream is not reused.
	if s.broadcasterEpoch != epoch || s.broadcastEpoch != epoch {
		return nil
	}

	return s.broadcaster
}
