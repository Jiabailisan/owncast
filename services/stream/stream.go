package stream

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/owncast/owncast/config"
	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/ingest"
	"github.com/owncast/owncast/services/notifications"
	"github.com/owncast/owncast/services/transcoder"
	"github.com/owncast/owncast/utils"
)

var (
	// errStreamStopped is the session close reason used when the whole stream
	// service shuts down.
	errStreamStopped = errors.New("stream service stopped")
	// errStreamOffline is the session close reason used when the live stream
	// ended.
	errStreamOffline = errors.New("stream went offline")
	// errPublishRefused reports that an ingest session could not be published; the
	// caller closes that session so its slot and sampler are released.
	errPublishRefused = errors.New("the ingest session could not be published")
	// errNoIngestSession reports a missing session from an ingest listener.
	errNoIngestSession = errors.New("no ingest session")
	// errSessionCancelled reports that the publisher went away while the WebRTC
	// copy plan was waiting for the source metadata. Nothing was published for
	// it: no online announcement, no variants, no media legs, no transcoder.
	errSessionCancelled = errors.New("the ingest session ended before its stream was published")
)

// Start brings up the storage backend, transcoder, ingest listeners, chat,
// webhooks, and the directory/notification subsystems. Must be called
// exactly once after New().
func (s *Service) Start(_ context.Context) error {
	s.resetDirectories()

	if err := s.configRepository.VerifySettings(s.cfg.TemporaryStreamKey); err != nil {
		log.Error(err)
		return err
	}

	if err := s.setupStats(); err != nil {
		log.Error("failed to setup the stats")
		return err
	}

	// The HLS handler takes the written HLS playlists and segments and
	// makes storage decisions. Rather simple right now but will play more
	// useful when recordings come into play.
	s.handler = transcoder.HLSHandler{
		OnSegmentWritten: s.recordHLSSegmentWritten,
	}

	if err := s.setupStorage(); err != nil {
		log.Errorln("storage error", err)
	}

	s.fileWriter.SetupFileWriterReceiverService(&s.handler)

	if err := s.createInitialOfflineState(); err != nil {
		log.Error("failed to create the initial offline state")
		return err
	}

	if err := s.chat.Start(); err != nil {
		log.Errorln(err)
	}

	// Begin accepting an inbound broadcast through the stream engine. The
	// local engine binds the in-process RTMP listener; lifecycle is reported
	// back to this service via the StreamEvents methods.
	if err := s.engine.Start(); err != nil {
		return err
	}

	rtmpPort := s.configRepository.GetRTMPPortNumber()
	if rtmpPort != 1935 {
		log.Infof("RTMP is accepting inbound streams on port %d.", rtmpPort)
	}

	s.webhooks.Start()

	return nil
}

// Stop releases everything the service is holding: force-disconnects the
// active ingest session, closes every ingest listener, and kills the ffmpeg
// process groups (live and offline) so shutdown cannot orphan an encoder.
func (s *Service) Stop(_ context.Context) {
	if s.sessions != nil {
		s.sessions.DisconnectCurrent(errStreamStopped)
	}
	if s.engine != nil {
		_ = s.engine.Stop()
	}
	if tr := s.getTranscoder(); tr != nil {
		tr.Stop()
	}
	s.stopWebRTCProcess()
	s.closeIngestTee()
	s.transcoderMu.Lock()
	offlineTr := s.offlineTranscoder
	s.offlineTranscoder = nil
	s.transcoderMu.Unlock()
	if offlineTr != nil {
		offlineTr.Stop()
	}
	s.lifecycleMu.Lock()
	if s.thumbnailGen != nil {
		s.thumbnailGen.Stop()
		s.thumbnailGen = nil
	}
	if s.onlineTimerCancelFunc != nil {
		s.onlineTimerCancelFunc()
		s.onlineTimerCancelFunc = nil
	}
	s.lifecycleMu.Unlock()
	s.StopOfflineCleanupTimer()
	s.stopOnlineCleanupTimer()
}

func (s *Service) createInitialOfflineState() error {
	s.transitionToOfflineVideoStreamContent()
	return nil
}

// transitionToOfflineVideoStreamContent overwrites the current stream
// with the offline video stream state only. No live stream HLS segments
// will continue to be referenced.
func (s *Service) transitionToOfflineVideoStreamContent() {
	log.Traceln("Firing transcoder with offline stream state")

	// Copy the logo to be the thumbnail
	logo := s.configRepository.GetLogoPath()
	dst := filepath.Join(s.cfg.TempDir, "thumbnail.jpg")
	if err := utils.Copy(filepath.Join("data", logo), dst); err != nil {
		log.Warnln(err)
	}

	// Delete the preview Gif
	_ = os.Remove(filepath.Join(s.cfg.TempDir, "preview.gif"))

	// WebRTC-only configurations have zero HLS variants; skip spawning an empty
	// offline HLS transcoder.
	if len(hlsEnabledVariants(s.configRepository.GetStreamOutputVariants())) == 0 {
		return
	}

	offlineTranscoder := transcoder.NewTranscoder(s.cfg, s.configRepository)
	offlineTranscoder.SetSharedH264Profile(s.configRepository.GetWebRTCConfig().H264Profile)
	offlineTranscoder.SetIdentifier("offline")
	offlineTranscoder.SetLatencyLevel(models.GetLatencyLevel(4))
	offlineTranscoder.SetIsEvent(true)

	offlineFilePath, err := saveOfflineClipToDisk(s.cfg.TempDir, "offline-v2.ts")
	if err != nil {
		log.Errorf("unable to save offline clip: %v", err)
		return
	}

	offlineTranscoder.SetInput(offlineFilePath)
	// Stop any previous offline process before starting a new one so repeated
	// offline transitions cannot accumulate ffmpeg children.
	s.transcoderMu.Lock()
	prev := s.offlineTranscoder
	s.offlineTranscoder = offlineTranscoder
	s.transcoderMu.Unlock()
	if prev != nil {
		prev.Stop()
	}
	go offlineTranscoder.Start(false)
}

func (s *Service) resetDirectories() {
	log.Trace("Resetting file directories to a clean slate.")

	// Wipe hls data directory
	utils.CleanupDirectory(config.HLSStoragePath)

	// Remove the previous thumbnail
	logo := s.configRepository.GetLogoPath()
	logoSrc := filepath.Join("data", logo)
	if logo != "" && utils.DoesFileExists(logoSrc) {
		err := utils.Copy(logoSrc, filepath.Join(s.cfg.TempDir, "thumbnail.jpg"))
		if err != nil {
			log.Warnln(err)
		}
	}
}

// HandleIngestSession is the protocol-neutral on-connect callback used by
// every ingest listener (RTMP and SRT). It reports the broadcast online
// (StreamConnected) and then spins up the in-process transcoder + thumbnail
// generator fed by the session's media bytes and input format.
func (s *Service) HandleIngestSession(session ingest.Session) error {
	if session == nil {
		return errNoIngestSession
	}

	broadcast := &models.CurrentBroadcast{
		LatencyLevel:   s.configRepository.GetStreamLatencyLevel(),
		OutputSettings: s.configRepository.GetStreamOutputVariants(),
	}
	// Refuse a session that is already gone or already replaced before reading
	// ahead for it: the publish boundary below re-checks this under the lock.
	if !s.canPublish(session) {
		log.Debugln("stream: refusing to publish an ingest session that already ended, lost the publisher slot or arrived during a teardown")

		return errPublishRefused
	}

	webrtcVariants := effectiveWebRTCVariants(broadcast.OutputSettings)

	// For an SRT stream with a pending copy decision, read ahead so the MPEG-TS
	// inspector can publish source metadata before the plan is fixed. The buffered
	// bytes are replayed into FFmpeg, so nothing is lost. The gate is shared with
	// the wait below: without WebRTC delivery there is no copy leg to decide, and
	// reading ahead for a verdict nothing consumes only delays the HLS start.
	mediaReader := session.Reader()
	// budgetSpent reports that the read-ahead budget is exhausted. Nothing drains
	// the buffer until the transcoder starts, so a copy decision pending at that
	// moment cannot gain evidence and must not sit out the full source wait.
	var budgetSpent <-chan struct{}
	var prefetch *prefetchReader
	if s.webRTCCopyPlanPending(session, webrtcVariants) {
		prefetch = newPrefetchReader(mediaReader)
		prefetch.Prefetch(time.Now().Add(webRTCSourceWaitTimeout), webRTCReadAheadMaxBytes)
		mediaReader = prefetch
		budgetSpent = prefetch.BudgetSpent()
	}

	// Resolve the source metadata the copy decision needs before the publish is
	// committed. A publisher that goes away during the wait must not be announced
	// online, publish variants, open media legs or start FFmpeg and the thumbnail
	// generator, and the existing publish boundary below already refuses a session
	// that is gone or that lost the slot.
	source, cancelled := s.resolveWebRTCSource(session, webrtcVariants, budgetSpent, prefetch)
	if cancelled {
		return errSessionCancelled
	}

	// Commit the publish at the hand-off boundary: if the session already ended or a
	// newer publisher owns the slot, nothing is announced and no transcoder starts.
	if !s.setCurrentSession(session) {
		// The caller closes the session it was refused: its sampler stops and its
		// publisher slot is released by the normal close path, so a later publisher
		// can take over.
		log.Debugln("stream: refusing to publish an ingest session that already ended, lost the publisher slot or arrived during a teardown")

		return errPublishRefused
	}
	s.StreamConnected(broadcast)

	// Prepare WebRTC delivery first: the transcoder plan includes the RTP
	// legs for the variants WebRTC exposes. Pass &mediaReader so a bitrate wait
	// can pump the ingest stream without losing bytes before FFmpeg starts.
	webrtc := s.configureWebRTC(session, &mediaReader, broadcast.OutputSettings, source)
	if webrtc.source.VideoBitrateKbps > 0 || (webrtc.source.Width > 0 && source.Width == 0) {
		source = webrtc.source
	}
	processModel := s.configRepository.GetOutputProcessModel()
	// Remember what this stream started with: the admin reports a pending
	// process-model change only when it differs from the running one.
	s.processModelMu.Lock()
	s.runningProcessModel = processModel
	s.processModelMu.Unlock()

	segmentPath := config.HLSStoragePath

	hlsVariants := hlsEnabledVariants(broadcast.OutputSettings)
	// Initialize thumbnail generator BEFORE launching the transcoder goroutine so
	// an immediate FFmpeg exit cannot race past s.thumbnailGen assignment and
	// leave an orphaned thumbnail ticker running while offline.
	s.lifecycleMu.Lock()
	if s.thumbnailGen != nil {
		s.thumbnailGen.Stop()
		s.thumbnailGen = nil
	}
	if len(hlsVariants) > 0 {
		selectedThumbnailVideoQualityIndex, isVideoPassthrough := s.configRepository.FindHighestVideoQualityIndex(hlsVariants)
		tg := transcoder.NewThumbnailGenerator(s.cfg, s.configRepository)
		tg.Start(segmentPath, selectedThumbnailVideoQualityIndex, isVideoPassthrough)
		s.thumbnailGen = tg
	}
	s.lifecycleMu.Unlock()

	go func() {
		if processModel == models.OutputProcessModelIsolated && len(webrtc.outputs) > 0 {
			s.runIsolatedTranscoders(session, mediaReader, webrtc)
			return
		}

		tr := transcoder.NewTranscoder(s.cfg, s.configRepository)
		webrtcCfg := s.configRepository.GetWebRTCConfig()
		tr.SetSharedH264Profile(webrtcCfg.H264Profile)
		tr.SetSharedH264Level(webrtcCfg.H264Level)
		if source.VideoCodec != "" {
			tr.SetSourceCodec(source.VideoCodec)
		} else if broadcaster := s.GetBroadcaster(); broadcaster != nil && broadcaster.StreamDetails.VideoCodec != "" {
			tr.SetSourceCodec(broadcaster.StreamDetails.VideoCodec)
		}
		if source.Width > 0 && source.Height > 0 {
			tr.SetSourceDetails(source.Width, source.Height, source.VideoBitrateKbps, source.FPS, source.VideoProfile, source.VideoLevel)
		} else if broadcaster := s.GetBroadcaster(); broadcaster != nil {
			details := broadcaster.StreamDetails
			tr.SetSourceDetails(details.Width, details.Height, details.VideoBitrate, float64(details.VideoFramerate), details.VideoProfile, details.VideoLevel)
		}
		tr.SetMasterPlaylistWrittenCallback(s.handler.MasterPlaylistWritten)
		var purpose []string
		if len(hlsVariants) > 0 {
			purpose = append(purpose, publicPipelineHLS)
		}
		if len(webrtc.outputs) > 0 {
			purpose = append(purpose, "webrtc")
		}
		tr.SetPurpose(purpose)
		tr.TranscoderCompleted = func(err error) {
			// Session-scoped: a completion from a stream that already lost the
			// broadcast must not stop the resources a newer stream installed.
			s.streamDisconnectedFor(session, err)
			s.clearTranscoder(tr)
		}
		tr.SetStdin(mediaReader)
		tr.SetInputFormat(session.InputFormat())
		tr.SetRTPOutputs(webrtc.outputs)
		tr.SetSourceVideoOnly(sessionVideoOnly(session))
		s.setTranscoder(tr)
		tr.Start(len(hlsVariants) > 0)
	}()

	return nil
}

// setCurrentSession publishes the session as the live one, but only while it is still
// open and still owns the single publisher slot. It reports whether it was published:
// a cancellation or a takeover that landed first wins, and the caller must not
// announce the stream or start a transcoder.
func (s *Service) setCurrentSession(session ingest.Session) bool {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()

	if !s.canPublish(session) {
		return false
	}
	s.currentSession = session

	return true
}

// canPublish reports whether the session may take the broadcast: it must still be
// open, still own the single publisher slot, and no teardown may be running. The
// caller that publishes re-checks it under the session lock; the startup path uses
// it as a fast path so it does not spend the source wait on a session that is
// already gone or already replaced.
func (s *Service) canPublish(session ingest.Session) bool {
	if session == nil {
		return true
	}
	if s.tearingDown.Load() {
		// The previous broadcast is still being cleaned up; its shared state must
		// not interleave with this publish.
		return false
	}
	if ingestSessionClosed(session) {
		return false
	}

	return s.sessions == nil || s.sessions.Current() == session
}

// clearTranscoder forgets the transcoder reference only while it still belongs to the
// caller. A completion from a finished stream must not clear the transcoder a newer
// stream installed.
func (s *Service) clearTranscoder(tr *transcoder.Transcoder) {
	s.transcoderMu.Lock()
	defer s.transcoderMu.Unlock()
	if s.transcoder == tr {
		s.transcoder = nil
	}
}

// clearCurrentSessionIfStale forgets the active session only when it is no longer a
// live publisher. A late offline from a finished stream must not clear the session a
// newer stream already published.
func (s *Service) clearCurrentSessionIfStale() {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()

	if s.currentSession == nil {
		return
	}
	if ingestSessionClosed(s.currentSession) {
		s.currentSession = nil

		return
	}
	if s.sessions != nil && s.sessions.Current() != s.currentSession {
		s.currentSession = nil
	}
}

// ingestSessionClosed reports whether the session already terminated.
func ingestSessionClosed(session ingest.Session) bool {
	select {
	case <-session.Done():
		return true
	default:
		return false
	}
}

// StreamConnected records a broadcast going live and runs the always-on
// go-live side effects. Implements StreamEvents; the local engine calls it
// in-process, a remote engine over its signaling channel.
func (s *Service) StreamConnected(broadcast *models.CurrentBroadcast) {
	// The local path published this session before announcing the stream; a remote engine
	// has none, which the offline and snapshot paths already tolerate.
	s.applyStreamOnline(broadcast, s.GetCurrentSession())
}

// applyStreamOnline runs the always-on side effects of a stream going live:
// stats, cleanup/notification timers, storage setup, and the chat / webhook /
// federation go-live announcements. It deliberately excludes the transcoder
// and thumbnail pipeline (engine work), so a remote engine can drive just
// these reactions through StreamConnected.
func (s *Service) applyStreamOnline(broadcast *models.CurrentBroadcast, session ingest.Session) {
	// Cache the warm-up window here: IsStreamConnected is called for every HLS
	// segment request and used to read the latency level from the datastore.
	waitSeconds := s.configRepository.GetStreamLatencyLevel().SecondsPerSegment * 3
	if waitSeconds < 7 {
		waitSeconds = 7
	}
	s.onlineWaitNanos.Store(int64(time.Duration(waitSeconds) * time.Second))

	now := utils.NullTime{Time: time.Now(), Valid: true}
	// One critical section publishes the settings, the broadcast identity and the
	// connectivity, so a snapshot cannot combine the previous settings with the new
	// epoch.
	s.statsMu.Lock()
	s.currentBroadcast = broadcast
	s.broadcastSession = session
	s.stats.StreamConnected = true
	s.broadcastEpoch++
	s.stats.LastDisconnectTime = nil
	s.stats.LastConnectTime = &now
	s.stats.SessionMaxViewerCount = 0
	s.statsMu.Unlock()

	s.StopOfflineCleanupTimer()

	// Initialize storage before starting the online cleanup ticker so the ticker
	// never races against a concurrent s.storage swap.
	if err := s.setupStorage(); err != nil {
		log.Fatalln("failed to setup the storage", err)
	}

	s.startOnlineCleanupTimer()

	if s.yp != nil {
		go s.yp.Start()
	}

	go s.webhooks.SendStreamStatusEvent(models.StreamStarted, s.currentStreamTitle())

	_ = s.chat.SendSystemAction("Stay tuned, the stream is **starting**!", true)
	s.chat.SendAllWelcomeMessage()

	// Send delayed notification messages.
	s.lifecycleMu.Lock()
	if s.onlineTimerCancelFunc != nil {
		s.onlineTimerCancelFunc()
	}
	s.onlineTimerCancelFunc = s.startLiveStreamNotificationsTimer()
	s.lifecycleMu.Unlock()

	// Let peer Owncast servers refresh their featured-streams listing.
	// Send one Offer immediately so a go-live propagates to followers
	// without waiting for the first periodic tick, then start the ticker
	// to keep the listing fresh for the duration of the stream.
	if s.configRepository.GetFederationEnabled() {
		if err := s.activitypub.SendStreamPing(); err != nil {
			log.Errorf("unable to send immediate go-live stream ping: %v", err)
		}
		s.activitypub.StartStreamPingTicker()
	}
}

// streamDisconnectedFor ends the broadcast on behalf of one session. The ownership
// check and the decision share the session lock, so a completion that arrives after a
// newer session took over does nothing at all.
func (s *Service) streamDisconnectedFor(session ingest.Session, err error) {
	// Identity check and the offline commit share the lock that guards publishing, so
	// a new session can neither be published in the middle of this teardown nor be
	// cleared by it.
	s.sessionMu.Lock()
	current := s.currentSession
	if session != nil && current != session {
		s.sessionMu.Unlock()
		log.Debugln("stream: ignoring a completion from a session that no longer owns the broadcast")

		return
	}
	// Only the call that commits the offline state runs the teardown. A second
	// completion for the same broadcast finds it already committed and does nothing,
	// so the shared cleanup and the gate release happen exactly once.
	if s.tearingDown.Load() {
		s.sessionMu.Unlock()
		log.Debugln("stream: a teardown for this broadcast is already running")

		return
	}
	s.tearingDown.Store(true)
	s.sessionMu.Unlock()

	defer s.tearingDown.Store(false)

	if err != nil {
		log.Debugln("stream: broadcast ended:", err)
	}

	s.applyStreamOffline(session)
}

// StreamDisconnected records a broadcast ending and clears the in-flight
// broadcast settings. Implements StreamEvents; the local engine calls it when
// the transcoder exits, a remote engine when its stream goes offline.
func (s *Service) StreamDisconnected(err error) {
	// A remote engine reports the end without naming a session, so it is attributed to
	// whoever owns the broadcast; the local paths pass the session explicitly.
	s.sessionMu.RLock()
	session := s.currentSession
	s.sessionMu.RUnlock()

	s.streamDisconnectedFor(session, err)
}

// IsStreamBusy reports whether a stream is live or still being torn down. The
// ingest listeners use it to decide whether a new publisher has to wait.
func (s *Service) IsStreamBusy() bool {
	return s.tearingDown.Load() || s.IsStreamConnected()
}

// beginOffline captures the broadcast settings this stream started with and publishes
// the offline state, both under statsMu. Callers see the stream as offline immediately;
// the teardown keeps using the returned settings to write the per-variant offline
// content.
func (s *Service) beginOffline() *models.CurrentBroadcast {
	now := utils.NullTime{Time: time.Now(), Valid: true}

	s.statsMu.Lock()
	defer s.statsMu.Unlock()

	broadcast := s.currentBroadcast
	s.stats.StreamConnected = false
	s.currentBroadcast = nil
	s.broadcastSession = nil
	s.broadcaster = nil
	s.broadcasterEpoch = 0
	s.stats.LastDisconnectTime = &now
	s.stats.LastConnectTime = nil

	return broadcast
}

// applyStreamOffline handles the always-on cleanup when a live stream ends.
func (s *Service) applyStreamOffline(session ingest.Session) {
	// The engine teardown has to run on every exit path. It used to sit after the
	// offline-clip render, so a clip that could not be written left the publisher
	// connected, the media legs open and the variants advertised for a stream that
	// had already ended.
	defer func() {
		// Cancel the delayed live-notification timer: a stream that ends within
		// its two-minute window would otherwise still announce "we are live" to
		// the fediverse and to push subscribers after it has already stopped.
		s.lifecycleMu.Lock()
		if s.onlineTimerCancelFunc != nil {
			s.onlineTimerCancelFunc()
			s.onlineTimerCancelFunc = nil
		}
		if s.thumbnailGen != nil {
			s.thumbnailGen.Stop()
			s.thumbnailGen = nil
		}
		s.lifecycleMu.Unlock()
		if s.sessions != nil {
			s.sessions.DisconnectIf(session, errStreamOffline)
		}
		s.stopWebRTCProcess()
		s.closeIngestTee()
		s.closeWebRTC()
		s.processModelMu.Lock()
		s.runningProcessModel = ""
		s.processModelMu.Unlock()
		s.clearHLSMeasuredBitrates()
		s.clearCurrentSessionIfStale()
	}()
	_ = s.chat.SendSystemAction("The stream is ending.", true)

	// Capture the settings this broadcast started with and publish the offline state
	// in the same critical section: every other reader sees the stream as offline
	// immediately, while the cleanup below keeps working from the captured settings.
	broadcast := s.beginOffline()

	// Stop the federated stream-ping ticker so we don't keep advertising
	// that we are live after the stream has ended, then tell followers we've
	// gone offline so they drop us from the live section of their
	// featured-streams directory immediately instead of waiting for the
	// staleness sweep.
	s.activitypub.StopStreamPingTicker()
	if s.configRepository.GetFederationEnabled() {
		if err := s.activitypub.SendStreamGoingOffline(); err != nil {
			log.Errorf("unable to send stream-offline Leave activity: %v", err)
		}
	}

	offlineFilename := "offline-v2.ts"

	offlineFilePath, err := saveOfflineClipToDisk(s.cfg.TempDir, offlineFilename)
	if err != nil {
		log.Errorln(err)
		return
	}

	if s.yp != nil {
		s.yp.Stop()
	}

	// If there is no current broadcast available the previous stream
	// likely failed for some reason. Don't try to append to it. Just
	// transition to offline.
	if broadcast == nil {
		s.stopOnlineCleanupTimer()
		s.transitionToOfflineVideoStreamContent()
		log.Errorln("unexpected nil currentBroadcast")
		return
	}

	hlsIndex := 0
	for _, variant := range broadcast.OutputSettings {
		if !variant.HLSEnabled() {
			continue
		}
		s.makeVariantIndexOffline(hlsIndex, offlineFilePath, offlineFilename)
		hlsIndex++
	}

	s.StartOfflineCleanupTimer()
	s.stopOnlineCleanupTimer()
	s.saveStats()

	go s.webhooks.SendStreamStatusEvent(models.StreamStopped, s.currentStreamTitle())
}

// StartOfflineCleanupTimer fires a cleanup after n minutes being
// disconnected.
func (s *Service) StartOfflineCleanupTimer() {
	s.StopOfflineCleanupTimer()
	s.statsMu.RLock()
	epoch := s.broadcastEpoch
	s.statsMu.RUnlock()
	timer := time.NewTimer(5 * time.Minute)
	done := make(chan struct{})
	s.offlineCleanupMu.Lock()
	s.offlineCleanupTimer = timer
	s.offlineCleanupDone = done
	s.offlineCleanupMu.Unlock()
	go func() {
		select {
		case <-timer.C:
			// Serialize with applyStreamOnline/setupStorage via lifecycleMu and
			// re-verify the broadcast epoch so an expired timer can never wipe
			// directories belonging to a newly reconnected live stream.
			s.lifecycleMu.Lock()
			s.statsMu.RLock()
			stillSameOfflineEpoch := !s.stats.StreamConnected && s.broadcastEpoch == epoch
			s.statsMu.RUnlock()
			if !stillSameOfflineEpoch || s.tearingDown.Load() {
				s.lifecycleMu.Unlock()
				return
			}
			// Set video to offline state
			s.resetDirectories()
			s.transitionToOfflineVideoStreamContent()
			s.lifecycleMu.Unlock()
		case <-done:
			return
		}
	}()
}

// StopOfflineCleanupTimer stops the previous offline cleanup timer and ends its
// goroutine. A Timer channel never closes, so without the done channel the
// goroutine would block forever and leak once per stream.
func (s *Service) StopOfflineCleanupTimer() {
	s.offlineCleanupMu.Lock()
	defer s.offlineCleanupMu.Unlock()
	if s.offlineCleanupTimer != nil {
		s.offlineCleanupTimer.Stop()
		s.offlineCleanupTimer = nil
	}
	if s.offlineCleanupDone != nil {
		close(s.offlineCleanupDone)
		s.offlineCleanupDone = nil
	}
}

func (s *Service) startOnlineCleanupTimer() {
	s.stopOnlineCleanupTimer()
	ticker := time.NewTicker(1 * time.Minute)
	done := make(chan struct{})
	s.onlineCleanupMu.Lock()
	s.onlineCleanupTicker = ticker
	s.onlineCleanupDone = done
	s.onlineCleanupMu.Unlock()
	go func() {
		for {
			select {
			case <-ticker.C:
				s.lifecycleMu.Lock()
				st := s.storage
				s.lifecycleMu.Unlock()
				if st != nil {
					if err := st.Cleanup(); err != nil {
						log.Errorln(err)
					}
				}
			case <-done:
				return
			}
		}
	}()
}

func (s *Service) stopOnlineCleanupTimer() {
	s.onlineCleanupMu.Lock()
	defer s.onlineCleanupMu.Unlock()
	if s.onlineCleanupTicker != nil {
		s.onlineCleanupTicker.Stop()
		s.onlineCleanupTicker = nil
	}
	if s.onlineCleanupDone != nil {
		close(s.onlineCleanupDone)
		s.onlineCleanupDone = nil
	}
}

// goLiveNotificationDelay is how long a stream must stay live before the federated
// Go Live message and the push notifications go out, so a short test stream does
// not notify every follower. It is cancelled outright if the stream ends first.
const goLiveNotificationDelay = 15 * time.Second

func (s *Service) startLiveStreamNotificationsTimer() context.CancelFunc {
	// Send delayed notification messages.
	c, cancelFunc := context.WithCancel(context.Background())
	s.onlineTimerCancelFunc = cancelFunc

	// The timer belongs to the stream that started it. If that stream ended or was replaced
	// before the delay elapsed, this notification is about a stream that is no longer live and
	// must not be sent on behalf of the new one.
	session := s.GetCurrentSession()

	go func(c context.Context) {
		select {
		case <-time.After(goLiveNotificationDelay):
			if s.lastNotified != nil && time.Since(*s.lastNotified) < 10*time.Minute {
				return
			}
			if s.GetCurrentSession() != session {
				return
			}

			// Send Fediverse message.
			if s.configRepository.GetFederationEnabled() {
				log.Traceln("Sending Federated Go Live message.")
				if err := s.activitypub.SendLive(); err != nil {
					log.Errorln(err)
				}
			}

			// Send notification to those who have registered for them.
			if notificationService, err := notifications.New(s.datastore, s.configRepository); err != nil {
				log.Errorln(err)
			} else {
				// The title is resolved now, not when the timer started: a publisher may have
				// set it in its stream key, and that overrides the saved default.
				notificationService.Notify(s.currentStreamTitle())
			}

			now := time.Now()
			s.lastNotified = &now
		case <-c.Done():
		}
	}(c)

	return cancelFunc
}
