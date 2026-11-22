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
func (s *Service) runIsolatedTranscoders(session ingest.Session, mediaReader io.Reader, webrtc webRTCOutputs) {
	tee := newIngestTee(mediaReader)
	s.setIngestTee(tee)

	hls := transcoder.NewTranscoder(s.cfg, s.configRepository)
	hls.SetSharedH264Profile(s.configRepository.GetWebRTCConfig().H264Profile)
	hls.SetPurpose([]string{publicPipelineHLS})
	hls.SetInputFormat(session.InputFormat())
	hls.SetStdin(tee.AddBranch("hls", true, nil))
	hls.TranscoderCompleted = func(err error) {
		// One entry point for both process models. It checks ownership under the lock,
		// runs the offline cleanup (which stops the WebRTC process and closes the tee),
		// and ignores a completion from a stream that already lost the broadcast.
		s.streamDisconnectedFor(session, err)
		s.clearTranscoder(hls)
	}
	s.setTranscoder(hls)

	// Claim the process reference for this session before its supervisor starts, so
	// a supervisor left over from the previous stream can no longer write to it.
	s.claimWebRTCProcessSession(session)

	go tee.Run()
	go hls.Start(true)
	go s.superviseWebRTCProcess(tee, session, webrtc, s.webRTCVariantGen)
}

// superviseWebRTCProcess runs the WebRTC-only FFmpeg process with a bounded
// restart policy. Its crash never interrupts HLS; on restart the media legs are
// marked so the RTP forwarder rebases sequence numbers/timestamps.
// webRTCBranchName is the ingest-tee branch feeding the WebRTC-only FFmpeg
// process. It is replaced on every supervised restart.
const webRTCBranchName = "webrtc"

func (s *Service) superviseWebRTCProcess(tee *ingestTee, session ingest.Session, webrtc webRTCOutputs, variantGen uint64) {
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
		tr.SetRTPOutputs(webrtc.outputs)
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
		if !s.registerWebRTCTranscoder(session, tr) {
			// The session ended, or a newer stream already owns the reference: this
			// process must not start and must not keep its ingest branch.
			s.releaseWebRTCProcessSession(session)
			tee.RemoveBranch(webRTCBranchName)

			return
		}
		go tr.Start(false)

		select {
		case err := <-completed:
			s.clearWebRTCTranscoder(tr)
			if err == nil {
				log.Traceln("isolated webrtc process exited cleanly")
				// The process is gone, so the renditions it fed cannot be delivered any
				// more: withdraw them through the same path the exhausted case uses.
				s.withdrawWebRTCVariants(tee, session, variantGen, webrtc.outputs)

				return
			}
			if attempt >= webRTCProcessMaxRestarts {
				log.Errorln("isolated webrtc process failed; WebRTC legs disabled for this stream:", err)
				// Withdraw this stream's renditions and tear down the sessions and legs
				// they fed. The generation check inside keeps this from touching a later
				// stream.
				s.withdrawWebRTCVariants(tee, session, variantGen, webrtc.outputs)
				// The warning belongs to the failure case only, and only while this
				// stream still owns the WebRTC state.
				s.appendWebRTCWarningsForGeneration(variantGen, models.StatusWarning{
					Code:    "webrtc_process_failed",
					Message: "The WebRTC process failed repeatedly; WebRTC delivery is disabled for this stream while HLS continues.",
				})

				return
			}
			log.Warnf("isolated webrtc process exited (attempt %d/%d); restarting in %s: %v",
				attempt+1, webRTCProcessMaxRestarts, backoff, err)
			// Rebase the forwarder when the replacement producer starts.
			markLegsRestart(webrtc.legs)
			// Wait for the backoff or for the session to end, whichever comes first.
			// Sleeping outright let a finished session start a replacement process
			// that then wrote into the next stream's media legs.
			select {
			case <-session.Done():
				s.clearWebRTCTranscoder(tr)
				s.releaseWebRTCProcessSession(session)
				tee.RemoveBranch(webRTCBranchName)

				return
			case <-time.After(backoff):
			}
			backoff *= 2
		case <-session.Done():
			tr.Stop()
			s.clearWebRTCTranscoder(tr)
			s.releaseWebRTCProcessSession(session)
			tee.RemoveBranch(webRTCBranchName)

			return
		}
	}
}

// variantIndices lists the variant indices a set of RTP outputs feeds.
func variantIndices(outputs []transcoder.RTPOutput) []int {
	indices := make([]int, 0, len(outputs))
	for _, output := range outputs {
		indices = append(indices, output.VariantIndex)
	}

	return indices
}

// withdrawWebRTCVariants removes this stream's renditions from WHEP and tears down the
// sessions and legs they fed. It is the single path for both a clean exit and an
// exhausted retry budget: either way the process that fed those renditions is gone, and
// the generation check inside DisableVariants keeps a finished stream from touching the
// next one.
func (s *Service) withdrawWebRTCVariants(tee *ingestTee, session ingest.Session, variantGen uint64, rtpOutputs []transcoder.RTPOutput) {
	s.webRTC.DisableVariants(variantGen, variantIndices(rtpOutputs))
	s.releaseWebRTCProcessSession(session)
	tee.RemoveBranch(webRTCBranchName)
}

// setWebRTCWarningsForGeneration records warnings only while the given publication is
// still the current one, so a supervisor from a finished stream cannot add warnings to
// the next stream's status.
func (s *Service) appendWebRTCWarningsForGeneration(generation uint64, warnings ...models.StatusWarning) {
	s.webRTCMu.Lock()
	defer s.webRTCMu.Unlock()

	// The check and the append share one critical section: reading the list, unlocking
	// and writing it back would drop warnings recorded in between.
	if generation != s.webRTCVariantGen {
		return
	}
	s.webRTCWarnings = append(s.webRTCWarnings, warnings...)
}

// markLegsRestart tells the given legs that a new RTP producer starts. The supervisor
// holds the leg objects it opened, so a retry never looks them up by index and cannot
// touch a later stream that reuses the same index.
func markLegsRestart(legs []WebRTCMediaLeg) {
	for _, leg := range legs {
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

// claimWebRTCProcessSession makes session the owner of the WebRTC process
// reference. A new stream always wins, so a supervisor left over from the previous
// stream can no longer install or clear a process.
func (s *Service) claimWebRTCProcessSession(session ingest.Session) {
	s.webRTCProcessMu.Lock()
	defer s.webRTCProcessMu.Unlock()
	s.webRTCProcessSession = session
	s.webRTCProcess = nil
}

// registerWebRTCTranscoder installs tr only while session still owns the reference
// and has not been cancelled. The ownership check and the assignment share one
// critical section, so a supervisor cannot pass the check and then overwrite a
// reference installed by a newer session in between.
func (s *Service) registerWebRTCTranscoder(session ingest.Session, tr *transcoder.Transcoder) bool {
	if session == nil {
		return false
	}

	select {
	case <-session.Done():
		return false
	default:
	}

	s.webRTCProcessMu.Lock()
	defer s.webRTCProcessMu.Unlock()

	if s.webRTCProcessSession != session {
		return false
	}
	s.webRTCProcess = tr

	return true
}

// clearWebRTCTranscoder forgets one process reference while it still belongs to the
// caller, and deliberately keeps the stream's ownership: a process that failed must
// be replaceable by the same stream after its backoff.
func (s *Service) clearWebRTCTranscoder(tr *transcoder.Transcoder) {
	s.webRTCProcessMu.Lock()
	defer s.webRTCProcessMu.Unlock()
	if s.webRTCProcess == tr {
		s.webRTCProcess = nil
	}
}

// releaseWebRTCProcessSession gives up the ownership a supervisor holds, once it
// stops supervising for good. The session check keeps a supervisor from the previous
// stream from releasing ownership a newer stream already claimed.
func (s *Service) releaseWebRTCProcessSession(session ingest.Session) {
	if session == nil {
		return
	}

	s.webRTCProcessMu.Lock()
	defer s.webRTCProcessMu.Unlock()

	if s.webRTCProcessSession != session {
		return
	}
	s.webRTCProcessSession = nil
	s.webRTCProcess = nil
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
