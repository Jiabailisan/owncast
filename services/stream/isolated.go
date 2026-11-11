package stream

import (
	"io"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/ingest"
	"github.com/owncast/owncast/services/transcoder"
)

const (
	// webRTCProcessMaxRestarts bounds how many times the isolated WebRTC
	// process is restarted for a single live stream (spec 3.4 bounded restart).
	webRTCProcessMaxRestarts = 2
	// webRTCProcessBackoff is the first restart delay; it doubles per attempt.
	webRTCProcessBackoff = 1 * time.Second
)

// runIsolatedTranscoders starts the isolated process model: one FFmpeg process
// for the HLS legs requesting the original HLS argv byte-for-byte, and a
// separate WebRTC-only process with its own decoder and low-latency input flags.
// Ingest bytes are fanned out by an ingestTee; the WebRTC branch can never stall
// or crash the HLS branch.
func (s *Service) runIsolatedTranscoders(session ingest.Session, mediaReader io.Reader, rtpOutputs []transcoder.RTPOutput) {
	tee := newIngestTee(mediaReader)
	s.setIngestTee(tee)

	hls := transcoder.NewTranscoder(s.cfg, s.configRepository)
	hls.SetSharedH264Profile(s.configRepository.GetWebRTCConfig().H264Profile)
	hls.SetPurpose([]string{publicPipelineHLS})
	hls.SetInputFormat(session.InputFormat())
	hls.SetStdin(tee.AddBranch("hls", true, nil))
	hls.TranscoderCompleted = func(err error) {
		s.StreamDisconnected(err)
		s.setTranscoder(nil)
		s.stopWebRTCProcess()
		s.closeIngestTee()
	}
	s.setTranscoder(hls)

	go tee.Run()
	go hls.Start(true)
	go s.superviseWebRTCProcess(tee, session, rtpOutputs)
}

// superviseWebRTCProcess runs the WebRTC-only FFmpeg process with a bounded
// restart policy. Its crash never interrupts HLS; on restart the media legs are
// marked so the RTP forwarder rebases sequence numbers/timestamps.
// webRTCBranchName is the ingest-tee branch feeding the WebRTC-only FFmpeg
// process. It is replaced on every supervised restart.
const webRTCBranchName = "webrtc"

func (s *Service) superviseWebRTCProcess(tee *ingestTee, session ingest.Session, rtpOutputs []transcoder.RTPOutput) {
	backoff := webRTCProcessBackoff

	for attempt := 0; ; attempt++ {
		// Drop the previous (dead) branch before adding its replacement so the
		// fan-out never keeps queueing into a process that is gone.
		tee.RemoveBranch(webRTCBranchName)
		tr := transcoder.NewTranscoder(s.cfg, s.configRepository)
		tr.SetSharedH264Profile(s.configRepository.GetWebRTCConfig().H264Profile)
		tr.SetPurpose([]string{"webrtc"})
		tr.SetRTPOnly(true)
		tr.SetInputFormat(session.InputFormat())
		tr.SetRTPOutputs(rtpOutputs)
		tr.SetStdin(tee.AddBranch(webRTCBranchName, false, func() {
			// Overflow: stop this process; HLS keeps running.
			tr.Stop()
		}))

		completed := make(chan error, 1)
		tr.TranscoderCompleted = func(err error) {
			// The channel is buffered for one report; a second one used to block the
			// caller goroutine forever.
			select {
			case completed <- err:
			default:
			}
		}
		s.setWebRTCTranscoder(tr)
		go tr.Start(false)

		select {
		case err := <-completed:
			s.setWebRTCTranscoder(nil)
			if err == nil {
				log.Traceln("isolated webrtc process exited cleanly")
				return
			}
			if attempt >= webRTCProcessMaxRestarts {
				log.Errorln("isolated webrtc process failed; WebRTC legs disabled for this stream:", err)
				s.setWebRTCWarnings(append(s.WebRTCWarnings(), models.StatusWarning{
					Code:    "webrtc_process_failed",
					Message: "The WebRTC process failed repeatedly; WebRTC delivery is disabled for this stream while HLS continues.",
				}))
				return
			}
			log.Warnf("isolated webrtc process exited (attempt %d/%d); restarting in %s: %v",
				attempt+1, webRTCProcessMaxRestarts, backoff, err)
			// Rebase the forwarder when the replacement producer starts.
			s.markWebRTCLegsRestart()
			time.Sleep(backoff)
			backoff *= 2
		case <-session.Done():
			tr.Stop()
			s.setWebRTCTranscoder(nil)
			return
		}
	}
}

// markWebRTCLegsRestart tells the WebRTC media legs a new RTP producer starts.
func (s *Service) markWebRTCLegsRestart() {
	for _, leg := range s.webRTCLegsByVariant() {
		leg.MarkRestart()
	}
}

// registerWebRTCLeg records an open media leg for restart rebasing.
func (s *Service) registerWebRTCLeg(variantIndex int, leg WebRTCMediaLeg) {
	s.legsMu.Lock()
	defer s.legsMu.Unlock()
	if s.legsByVariant == nil {
		s.legsByVariant = map[int]WebRTCMediaLeg{}
	}
	s.legsByVariant[variantIndex] = leg
}

// clearWebRTCLegs drops the recorded media legs.
func (s *Service) clearWebRTCLegs() {
	s.legsMu.Lock()
	defer s.legsMu.Unlock()
	s.legsByVariant = nil
}

// webRTCLegsByVariant returns a snapshot of the open media legs.
func (s *Service) webRTCLegsByVariant() []WebRTCMediaLeg {
	s.legsMu.RLock()
	defer s.legsMu.RUnlock()
	legs := make([]WebRTCMediaLeg, 0, len(s.legsByVariant))
	for _, leg := range s.legsByVariant {
		legs = append(legs, leg)
	}
	return legs
}

// setIngestTee records the active tee (isolated process model).
func (s *Service) setIngestTee(tee *ingestTee) {
	s.ingestTeeMu.Lock()
	defer s.ingestTeeMu.Unlock()
	s.ingestTee = tee
}

// closeIngestTee closes and clears the active tee.
func (s *Service) closeIngestTee() {
	s.ingestTeeMu.Lock()
	tee := s.ingestTee
	s.ingestTee = nil
	s.ingestTeeMu.Unlock()
	if tee != nil {
		tee.Close()
	}
}

// setWebRTCTranscoder records the isolated WebRTC-only FFmpeg process.
func (s *Service) setWebRTCTranscoder(tr *transcoder.Transcoder) {
	s.webRTCProcessMu.Lock()
	defer s.webRTCProcessMu.Unlock()
	s.webRTCProcess = tr
}

// getWebRTCTranscoder returns the isolated WebRTC-only FFmpeg process, if any.
func (s *Service) getWebRTCTranscoder() *transcoder.Transcoder {
	s.webRTCProcessMu.RLock()
	defer s.webRTCProcessMu.RUnlock()
	return s.webRTCProcess
}

// stopWebRTCProcess stops the isolated WebRTC-only process (idempotent).
func (s *Service) stopWebRTCProcess() {
	tr := s.getWebRTCTranscoder()
	if tr != nil {
		tr.Stop()
	}
}
