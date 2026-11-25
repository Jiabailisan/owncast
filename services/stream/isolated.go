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
	webrtcCfg := s.configRepository.GetWebRTCConfig()
	hls.SetSharedH264Profile(webrtcCfg.H264Profile)
	hls.SetSharedH264Level(webrtcCfg.H264Level)
	hls.SetPurpose([]string{publicPipelineHLS})
	hls.SetInputFormat(session.InputFormat())
	// The HLS process needs the flag too: its var_stream_map and tee select
	// specifiers name the audio stream, and a video-only source has none.
	hls.SetSourceVideoOnly(sessionVideoOnly(session))
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

	// Register the first WebRTC branch before the pump starts. The stream head
	// (PAT/PMT and the H.264 parameter sets) passes within the first tens of
	// milliseconds; a branch added after the pump began never sees it, and its
	// FFmpeg probe fails with "non-existing PPS 0" / "unspecified size".
	// Give Attempt 0 extra byte budget matching any prefetched head bytes so
	// replaying a >4 MiB read-ahead buffer while FFmpeg spawns cannot trigger a
	// spurious overflow before Attempt 0 begins reading stdin.
	headBudget := int64(bufferedPrefetchBytes(mediaReader))
	webrtcBranch := tee.AddLateBranchWithBudget(webRTCBranchName, false, nil, session.InputFormat().Name, containerPrefix(session), headBudget)
	if webrtcBranch == nil {
		// Registration does not fail in practice, but if it ever did, the HLS
		// path must still start: the pump only needs the critical branch. The
		// supervisor handles a nil first branch by registering late.
		log.Warnln("ingest tee: the first WebRTC branch could not be registered; it will join mid-stream")
	}

	// Both branches exist, so the pump can start: everything from the first byte
	// now reaches both processes.
	tee.Start()

	go hls.Start(true)
	go s.superviseWebRTCProcess(tee, session, webrtc, webrtc.variantGen, webrtcBranch)
}

// superviseWebRTCProcess runs the WebRTC-only FFmpeg process with a bounded
// restart policy. Its crash never interrupts HLS; on restart the media legs are
// marked so the RTP forwarder rebases sequence numbers/timestamps.
// webRTCBranchName is the ingest-tee branch feeding the WebRTC-only FFmpeg
// process. It is replaced on every supervised restart.
const webRTCBranchName = "webrtc"

func (s *Service) superviseWebRTCProcess(tee *ingestTee, session ingest.Session, webrtc webRTCOutputs, variantGen uint64, firstBranch *io.PipeReader) {
	backoff := webRTCProcessBackoff

	for attempt := 0; ; attempt++ {
		// Attempt 0 reuses the branch that was registered before the pump started
		// (it saw the stream head); later attempts join mid-stream, so they get a
		// fresh, container-aligned branch after dropping the dead one.
		var branch *io.PipeReader
		if attempt == 0 && firstBranch != nil {
			branch = firstBranch
		} else {
			if attempt == 0 {
				log.Warnln("isolated webrtc: the first branch was not pre-registered; joining mid-stream")
			}
			// Drop the previous (dead) branch before adding its replacement so the
			// fan-out never keeps queueing into a process that is gone.
			tee.RemoveBranch(webRTCBranchName)
			branch = tee.AddLateBranch(webRTCBranchName, false, nil, session.InputFormat().Name, containerPrefix(session))
		}
		tr := transcoder.NewTranscoder(s.cfg, s.configRepository)
		webrtcCfg := s.configRepository.GetWebRTCConfig()
		tr.SetSharedH264Profile(webrtcCfg.H264Profile)
		tr.SetSharedH264Level(webrtcCfg.H264Level)
		tr.SetPurpose([]string{"webrtc"})
		tr.SetRestartCount(attempt)
		tr.SetRTPOnly(true)
		tr.SetInputFormat(session.InputFormat())
		tr.SetRTPOutputs(webrtc.outputs)
		tr.SetSourceVideoOnly(sessionVideoOnly(session))
		tr.SetStdin(branch)
		// Overflow on this branch stops this process; HLS keeps running. The
		// first branch was registered before tr existed, so the callback is bound
		// here for every attempt.
		if branchHandle := tee.Branch(webRTCBranchName); branchHandle != nil {
			branchHandle.SetOverflow(func() { tr.Stop() })
		}

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

// containerPrefix returns the bytes a late-joining process must receive before
// the live stream: the container header plus the decoder configuration the
// session cached. Containers that repeat their parameter sets in band need none.
func containerPrefix(session ingest.Session) []byte {
	provider, ok := session.(ingest.ContainerPrefixProvider)
	if !ok {
		return nil
	}

	return provider.ContainerPrefix()
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
	withdrawn := variantIndices(rtpOutputs)
	s.webRTC.DisableVariants(variantGen, withdrawn)
	s.markWebRTCLegsDisabledForGeneration(variantGen, withdrawn)
	s.releaseWebRTCProcessSession(session)
	tee.RemoveBranch(webRTCBranchName)
}

// markWebRTCLegsDisabledForGeneration reflects withdrawn isolated WebRTC legs
// in the admin live-status leg snapshot while this stream generation is active.
func (s *Service) markWebRTCLegsDisabledForGeneration(generation uint64, indices []int) {
	if len(indices) == 0 {
		return
	}
	dropped := make(map[int]struct{}, len(indices))
	for _, index := range indices {
		dropped[index] = struct{}{}
	}

	s.webRTCMu.Lock()
	defer s.webRTCMu.Unlock()
	if generation != s.webRTCVariantGen {
		return
	}
	for index := range s.webRTCLegsStatus {
		if _, ok := dropped[s.webRTCLegsStatus[index].VariantIndex]; ok {
			s.webRTCLegsStatus[index].Mode = models.WebRTCLegModeDisabled
			s.webRTCLegsStatus[index].OutputBitrateKbps = nil
		}
	}
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
