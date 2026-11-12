package stream

import (
	"context"
	"errors"
	"os"
	"path"
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
	s.handler = transcoder.HLSHandler{}

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
	if s.offlineTranscoder != nil {
		s.offlineTranscoder.Stop()
	}
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

	offlineTranscoder := transcoder.NewTranscoder(s.cfg, s.configRepository)
	offlineTranscoder.SetSharedH264Profile(s.configRepository.GetWebRTCConfig().H264Profile)
	offlineTranscoder.SetIdentifier("offline")
	offlineTranscoder.SetLatencyLevel(models.GetLatencyLevel(4))
	offlineTranscoder.SetIsEvent(true)

	offlineFilePath, err := saveOfflineClipToDisk(s.cfg.TempDir, "offline-v2.ts")
	if err != nil {
		log.Fatalln("unable to save offline clip:", err)
	}

	offlineTranscoder.SetInput(offlineFilePath)
	// Stop any previous offline process before starting a new one so repeated
	// offline transitions cannot accumulate ffmpeg children.
	if s.offlineTranscoder != nil {
		s.offlineTranscoder.Stop()
	}
	s.offlineTranscoder = offlineTranscoder
	go offlineTranscoder.Start(false)

	// Copy the logo to be the thumbnail
	logo := s.configRepository.GetLogoPath()
	dst := filepath.Join(s.cfg.TempDir, "thumbnail.jpg")
	if err = utils.Copy(filepath.Join("data", logo), dst); err != nil {
		log.Warnln(err)
	}

	// Delete the preview Gif
	_ = os.Remove(path.Join(config.DataDirectory, "preview.gif"))
}

func (s *Service) resetDirectories() {
	log.Trace("Resetting file directories to a clean slate.")

	// Wipe hls data directory
	utils.CleanupDirectory(config.HLSStoragePath)

	// Remove the previous thumbnail
	logo := s.configRepository.GetLogoPath()
	if utils.DoesFileExists(logo) {
		err := utils.Copy(path.Join("data", logo), filepath.Join(config.DataDirectory, "thumbnail.jpg"))
		if err != nil {
			log.Warnln(err)
		}
	}
}

// HandleIngestSession is the protocol-neutral on-connect callback used by
// every ingest listener (RTMP and SRT). It reports the broadcast online
// (StreamConnected) and then spins up the in-process transcoder + thumbnail
// generator fed by the session's media bytes and input format.
func (s *Service) HandleIngestSession(session ingest.Session) {
	if session == nil {
		return
	}

	broadcast := &models.CurrentBroadcast{
		LatencyLevel:   s.configRepository.GetStreamLatencyLevel(),
		OutputSettings: s.configRepository.GetStreamOutputVariants(),
	}
	s.setCurrentSession(session)
	s.StreamConnected(broadcast)

	// For an SRT stream with a pending copy decision, read ahead so the
	// MPEG-TS inspector can publish source metadata before the plan is fixed.
	// The buffered bytes are replayed into FFmpeg, so nothing is lost.
	mediaReader := session.Reader()
	if session.Protocol() == ingest.ProtocolSRT &&
		hasWebRTCCopyIntent(effectiveWebRTCVariants(broadcast.OutputSettings)) {
		prefetch := newPrefetchReader(mediaReader)
		prefetch.Prefetch(1 << 20)
		mediaReader = prefetch
	}

	// Prepare WebRTC delivery first: the transcoder plan includes the RTP
	// legs for the variants WebRTC exposes.
	rtpOutputs := s.configureWebRTC(session, broadcast.OutputSettings)
	processModel := s.configRepository.GetOutputProcessModel()
	// Remember what this stream started with: the admin reports a pending
	// process-model change only when it differs from the running one.
	s.processModelMu.Lock()
	s.runningProcessModel = processModel
	s.processModelMu.Unlock()

	segmentPath := config.HLSStoragePath

	go func() {
		if processModel == models.OutputProcessModelIsolated && len(rtpOutputs) > 0 {
			s.runIsolatedTranscoders(session, mediaReader, rtpOutputs)
			return
		}

		tr := transcoder.NewTranscoder(s.cfg, s.configRepository)
		tr.SetSharedH264Profile(s.configRepository.GetWebRTCConfig().H264Profile)
		purpose := []string{publicPipelineHLS}
		if len(rtpOutputs) > 0 {
			purpose = append(purpose, "webrtc")
		}
		tr.SetPurpose(purpose)
		tr.TranscoderCompleted = func(err error) {
			s.StreamDisconnected(err)
			s.setTranscoder(nil)
		}
		tr.SetStdin(mediaReader)
		tr.SetInputFormat(session.InputFormat())
		tr.SetRTPOutputs(rtpOutputs)
		s.setTranscoder(tr)
		tr.Start(true)
	}()

	selectedThumbnailVideoQualityIndex, isVideoPassthrough := s.configRepository.FindHighestVideoQualityIndex(broadcast.OutputSettings)
	s.thumbnailGen = transcoder.NewThumbnailGenerator(s.cfg, s.configRepository)
	s.thumbnailGen.Start(segmentPath, selectedThumbnailVideoQualityIndex, isVideoPassthrough)
}

// setCurrentSession replaces the active ingest session under the lock.
func (s *Service) setCurrentSession(session ingest.Session) {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	s.currentSession = session
}

// StreamConnected records a broadcast going live and runs the always-on
// go-live side effects. Implements StreamEvents; the local engine calls it
// in-process, a remote engine over its signaling channel.
func (s *Service) StreamConnected(broadcast *models.CurrentBroadcast) {
	s.currentBroadcast = broadcast
	s.applyStreamOnline()
}

// applyStreamOnline runs the always-on side effects of a stream going live:
// stats, cleanup/notification timers, storage setup, and the chat / webhook /
// federation go-live announcements. It deliberately excludes the transcoder
// and thumbnail pipeline (engine work), so a remote engine can drive just
// these reactions through StreamConnected.
func (s *Service) applyStreamOnline() {
	// Cache the warm-up window here: IsStreamConnected is called for every HLS
	// segment request and used to read the latency level from the datastore.
	waitSeconds := s.configRepository.GetStreamLatencyLevel().SecondsPerSegment * 3
	if waitSeconds < 7 {
		waitSeconds = 7
	}
	s.onlineWaitNanos.Store(int64(time.Duration(waitSeconds) * time.Second))

	now := utils.NullTime{Time: time.Now(), Valid: true}
	s.statsMu.Lock()
	s.stats.StreamConnected = true
	s.stats.LastDisconnectTime = nil
	s.stats.LastConnectTime = &now
	s.stats.SessionMaxViewerCount = 0
	s.statsMu.Unlock()

	s.StopOfflineCleanupTimer()
	s.startOnlineCleanupTimer()

	if s.yp != nil {
		go s.yp.Start()
	}

	if err := s.setupStorage(); err != nil {
		log.Fatalln("failed to setup the storage", err)
	}

	go s.webhooks.SendStreamStatusEvent(models.StreamStarted)

	_ = s.chat.SendSystemAction("Stay tuned, the stream is **starting**!", true)
	s.chat.SendAllWelcomeMessage()

	// Send delayed notification messages.
	s.onlineTimerCancelFunc = s.startLiveStreamNotificationsTimer()

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

// StreamDisconnected records a broadcast ending and clears the in-flight
// broadcast settings. Implements StreamEvents; the local engine calls it when
// the transcoder exits, a remote engine when its stream goes offline.
func (s *Service) StreamDisconnected(_ error) {
	// Published to the ingest accept path for the whole teardown, so a publisher
	// that reconnects immediately waits for this session to be fully gone.
	s.tearingDown.Store(true)
	defer s.tearingDown.Store(false)

	s.applyStreamOffline()
	s.currentBroadcast = nil
}

// IsStreamBusy reports whether a stream is live or still being torn down. The
// ingest listeners use it to decide whether a new publisher has to wait.
func (s *Service) IsStreamBusy() bool {
	return s.tearingDown.Load() || s.IsStreamConnected()
}

// applyStreamOffline handles the always-on cleanup when a live stream ends.
func (s *Service) applyStreamOffline() {
	// The engine teardown has to run on every exit path. It used to sit after the
	// offline-clip render, so a clip that could not be written left the publisher
	// connected, the media legs open and the variants advertised for a stream that
	// had already ended.
	defer func() {
		// Cancel the delayed live-notification timer: a stream that ends within
		// its two-minute window would otherwise still announce "we are live" to
		// the fediverse and to push subscribers after it has already stopped.
		if s.onlineTimerCancelFunc != nil {
			s.onlineTimerCancelFunc()
			s.onlineTimerCancelFunc = nil
		}
		if s.thumbnailGen != nil {
			s.thumbnailGen.Stop()
		}
		if s.sessions != nil {
			s.sessions.DisconnectCurrent(errStreamOffline)
		}
		s.stopWebRTCProcess()
		s.closeIngestTee()
		s.closeWebRTC()
		s.setCurrentSession(nil)
	}()
	_ = s.chat.SendSystemAction("The stream is ending.", true)

	now := utils.NullTime{Time: time.Now(), Valid: true}
	s.statsMu.Lock()
	s.stats.StreamConnected = false
	s.stats.LastDisconnectTime = &now
	s.stats.LastConnectTime = nil
	s.statsMu.Unlock()
	s.broadcaster = nil

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
	if s.currentBroadcast == nil {
		s.stopOnlineCleanupTimer()
		s.transitionToOfflineVideoStreamContent()
		log.Errorln("unexpected nil currentBroadcast")
		return
	}

	for index := range s.currentBroadcast.OutputSettings {
		s.makeVariantIndexOffline(index, offlineFilePath, offlineFilename)
	}

	s.StartOfflineCleanupTimer()
	s.stopOnlineCleanupTimer()
	s.saveStats()

	go s.webhooks.SendStreamStatusEvent(models.StreamStopped)
}

// StartOfflineCleanupTimer fires a cleanup after n minutes being
// disconnected.
func (s *Service) StartOfflineCleanupTimer() {
	s.StopOfflineCleanupTimer()
	timer := time.NewTimer(5 * time.Minute)
	done := make(chan struct{})
	s.offlineCleanupTimer = timer
	s.offlineCleanupDone = done
	go func() {
		select {
		case <-timer.C:
			// Set video to offline state
			s.resetDirectories()
			s.transitionToOfflineVideoStreamContent()
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
				if err := s.storage.Cleanup(); err != nil {
					log.Errorln(err)
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

func (s *Service) startLiveStreamNotificationsTimer() context.CancelFunc {
	// Send delayed notification messages.
	c, cancelFunc := context.WithCancel(context.Background())
	s.onlineTimerCancelFunc = cancelFunc
	go func(c context.Context) {
		select {
		case <-time.After(time.Minute * 2.0):
			if s.lastNotified != nil && time.Since(*s.lastNotified) < 10*time.Minute {
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
				notificationService.Notify()
			}

			now := time.Now()
			s.lastNotified = &now
		case <-c.Done():
		}
	}(c)

	return cancelFunc
}
