package webrtc

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/owncast/owncast/models"
	"github.com/pion/rtp"
	log "github.com/sirupsen/logrus"
)

// Payload types used on the FFmpeg loopback legs. The transcoder plan builder
// must use these exact values in the FFmpeg argv (-payload_type); Pion rewrites
// them to whatever each viewer negotiated.
const (
	PayloadTypeVideo = 96
	PayloadTypeAudio = 111
)

const (
	loopbackHost          = "127.0.0.1"
	forwarderReadBuffer   = 2048
	forwarderReadDeadline = 200 * time.Millisecond
	defaultVariantFPS     = 30
	audioFrameSamples     = 960 // 20 ms at 48 kHz
	forwarderKindVideo    = "video"

	// loopbackReadBufferBytes is the SO_RCVBUF request for the FFmpeg loopback
	// RTP sockets. Linux clamps it to net.core.rmem_max.
	loopbackReadBufferBytes = 8 << 20
)

// MediaLegStats is the health snapshot of one loopback media leg.
type MediaLegStats struct {
	Variant               int     `json:"variant"`
	VideoPackets          uint64  `json:"videoPackets"`
	AudioPackets          uint64  `json:"audioPackets"`
	VideoBytes            uint64  `json:"videoBytes"`
	AudioBytes            uint64  `json:"audioBytes"`
	ParseErrors           uint64  `json:"parseErrors"`
	PayloadTypeMismatches uint64  `json:"payloadTypeMismatches"`
	WriteErrors           uint64  `json:"writeErrors"`
	RebaseCount           uint64  `json:"rebaseCount"`
	ReorderedPackets      uint64  `json:"reorderedPackets"`
	MeasuredVideoFPS      float64 `json:"measuredVideoFps"`
	SPSSeen               bool    `json:"spsSeen"`
	PPSSeen               bool    `json:"ppsSeen"`
	LastKeyframeAgeMs     int64   `json:"lastKeyframeAgeMs"`
	LastPacketAgeMs       int64   `json:"lastPacketAgeMs"`
	VideoLastSequence     uint16  `json:"videoLastSequence"`
	VideoLastTimestamp    uint32  `json:"videoLastTimestamp"`
	AudioLastSequence     uint16  `json:"audioLastSequence"`
	AudioLastTimestamp    uint32  `json:"audioLastTimestamp"`
}

type forwarderCounters struct {
	packets     atomic.Uint64
	bytes       atomic.Uint64
	frames      atomic.Uint64
	parseErrors atomic.Uint64
	ptMismatch  atomic.Uint64
	writeErrors atomic.Uint64
	rebases     atomic.Uint64
	reordered   atomic.Uint64
	lastPacket  atomic.Int64 // unix nanos
	lastIDR     atomic.Int64 // unix nanos
}

// trackWriter is the subset of webrtc.TrackLocalStaticRTP the forwarder needs.
// It is an interface so the forwarder can be unit-tested with a recording
// writer; production always passes the shared Pion track.
type trackWriter interface {
	WriteRTP(packet *rtp.Packet) error
}

type forwarder struct {
	kind       string
	conn       *net.UDPConn
	track      trackWriter
	expectedPT uint8

	normalizer *normalizer
	reorder    *reorderBuffer

	counters forwarderCounters

	spsMu sync.RWMutex
	sps   []byte
	pps   []byte

	lastSeq atomic.Uint32
	lastTS  atomic.Uint32

	startedAt time.Time
}

func newForwarder(kind string, conn *net.UDPConn, track trackWriter,
	clockRate, expectedDelta uint32, payloadType uint8, reorderWindow time.Duration,
) *forwarder {
	f := &forwarder{
		kind:       kind,
		conn:       conn,
		track:      track,
		expectedPT: payloadType,
		normalizer: newNormalizer(clockRate, expectedDelta),
		startedAt:  time.Now(),
	}
	if reorderWindow > 0 {
		f.reorder = newReorderBuffer(reorderWindow)
	}

	return f
}

func (f *forwarder) start(wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		f.run()
	}()
}

func (f *forwarder) run() {
	buf := make([]byte, forwarderReadBuffer)
	for {
		if err := f.conn.SetReadDeadline(time.Now().Add(forwarderReadDeadline)); err != nil {
			return
		}
		n, _, err := f.conn.ReadFromUDP(buf)
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				f.flushReorderBuffer(time.Now())

				continue
			}

			return
		}
		f.handleRaw(buf[:n])
	}
}

func (f *forwarder) handleRaw(raw []byte) {
	packet := &rtp.Packet{}
	if err := packet.Unmarshal(raw); err != nil {
		f.counters.parseErrors.Add(1)

		return
	}

	now := time.Now()
	f.counters.packets.Add(1)
	f.counters.bytes.Add(uint64(len(raw)))
	f.counters.lastPacket.Store(now.UnixNano())

	if packet.PayloadType != f.expectedPT {
		f.counters.ptMismatch.Add(1)
	}
	if f.kind == forwarderKindVideo {
		f.inspectH264(packet.Payload)
		if packet.Marker {
			f.counters.frames.Add(1)
		}
	}

	if f.reorder == nil {
		f.emit(packet)

		return
	}
	f.emitReady(f.reorder.push(packet, now), false)
}

func (f *forwarder) emitReady(packets []*rtp.Packet, forced bool) {
	if forced {
		f.counters.reordered.Add(uint64(len(packets)))
	}
	for _, packet := range packets {
		f.emit(packet)
	}
}

func (f *forwarder) flushReorderBuffer(now time.Time) {
	if f.reorder == nil {
		return
	}
	ready, forced := f.reorder.flush(now)
	f.emitReady(ready, forced)
}

func (f *forwarder) emit(packet *rtp.Packet) {
	rebased := f.normalizer.apply(packet)
	if rebased {
		f.counters.rebases.Add(1)
		log.Debugf("webrtc: %s forwarder rebased RTP stream", f.kind)
	}
	// Copy legs already carry SPS/PPS on every keyframe because the copy RTP
	// output applies h264_mp4toannexb,dump_extra. Do not inject extra parameter
	// sets or renumber the sequence: renumbering hides packets that the loopback
	// socket lost, so the receiver assembles incomplete frames instead of asking
	// for a keyframe (green/partial pictures). Keeping the producer sequence
	// leaves gaps visible to the receiver's NACK/PLI logic.
	if err := f.track.WriteRTP(packet); err != nil {
		f.counters.writeErrors.Add(1)

		return
	}
	f.lastSeq.Store(uint32(packet.SequenceNumber))
	f.lastTS.Store(packet.Timestamp)
}

func (f *forwarder) markRestart() {
	f.normalizer.markRestart()
}

func (f *forwarder) inspectH264(payload []byte) {
	types := h264NALTypes(payload)
	if len(types) == 0 {
		return
	}
	sps, pps := h264ParameterSets(payload)
	if len(sps) > 0 {
		f.spsMu.Lock()
		f.sps = sps
		f.spsMu.Unlock()
	}
	if len(pps) > 0 {
		f.spsMu.Lock()
		f.pps = pps
		f.spsMu.Unlock()
	}
	for _, nalType := range types {
		if nalType == nalTypeIDR {
			f.counters.lastIDR.Store(time.Now().UnixNano())
		}
	}
}

func (f *forwarder) parameterSets() (sps, pps []byte) {
	f.spsMu.RLock()
	defer f.spsMu.RUnlock()

	return f.sps, f.pps
}

func (f *forwarder) stats() (packets, bytes uint64, parseErrors, ptMismatch, writeErrors, rebases uint64,
	lastSeq uint16, lastTS uint32, lastPacketAt time.Time,
) {
	packets = f.counters.packets.Load()
	bytes = f.counters.bytes.Load()
	parseErrors = f.counters.parseErrors.Load()
	ptMismatch = f.counters.ptMismatch.Load()
	writeErrors = f.counters.writeErrors.Load()
	rebases = f.counters.rebases.Load()
	lastSeq = uint16(f.lastSeq.Load()) //nolint:gosec // RTP sequence number is 16 bit
	lastTS = f.lastTS.Load()
	lastPacketAt = time.Unix(0, f.counters.lastPacket.Load())

	return
}

func (f *forwarder) lastIDRAt() time.Time {
	return time.Unix(0, f.counters.lastIDR.Load())
}

// MediaLeg is one WebRTC media leg: two loopback UDP sockets that Go owns and
// that FFmpeg writes RTP into, plus the forwarder goroutines that rewrite the
// counters and feed the shared Pion tracks. The sockets are bound before the
// leg is returned so the transcoder can put the URLs into the FFmpeg argv
// immediately; the forwarders are already reading when FFmpeg starts.
type MediaLeg struct {
	service *Service
	variant int
	cfg     models.WebRTCConfig

	videoConn *net.UDPConn
	audioConn *net.UDPConn

	videoForwarder *forwarder
	audioForwarder *forwarder

	wg        sync.WaitGroup
	closeOnce sync.Once
}

// OpenMediaLeg binds the two loopback UDP sockets for one WebRTC variant and
// starts the forwarders. It replaces and closes any previous leg for the same
// variant index.
func (s *Service) OpenMediaLeg(variantIndex int) (*MediaLeg, error) {
	if !s.Enabled() {
		return nil, ErrDisabled
	}
	tracks, ok := s.variantFor(variantIndex)
	if !ok {
		return nil, fmt.Errorf("%w: %d", ErrVariantNotFound, variantIndex)
	}

	cfg := s.Config()
	videoConn, err := listenLoopbackUDP()
	if err != nil {
		return nil, fmt.Errorf("bind webrtc video rtp socket: %w", err)
	}
	audioConn, err := listenLoopbackUDP()
	if err != nil {
		_ = videoConn.Close()

		return nil, fmt.Errorf("bind webrtc audio rtp socket: %w", err)
	}

	leg := &MediaLeg{
		service:   s,
		variant:   variantIndex,
		cfg:       cfg,
		videoConn: videoConn,
		audioConn: audioConn,
	}

	fps := tracks.variant.FPS
	if fps <= 0 {
		fps = defaultVariantFPS
	}
	videoDelta := rtpClockRate / uint32(fps) //nolint:gosec // fps is a small positive int
	reorderWindow := time.Duration(cfg.RTPReorderBufferMs) * time.Millisecond

	leg.videoForwarder = newForwarder(forwarderKindVideo, videoConn, tracks.video,
		rtpClockRate, videoDelta, PayloadTypeVideo, reorderWindow)
	leg.audioForwarder = newForwarder("audio", audioConn, tracks.audio,
		opusClockRate, audioFrameSamples, PayloadTypeAudio, reorderWindow)

	// Start the forwarders before publishing the leg: CloseMediaLeg/Stop can
	// otherwise take the leg and run wg.Wait() before the goroutines are
	// tracked, leaking them against closed sockets.
	leg.videoForwarder.start(&leg.wg)
	leg.audioForwarder.start(&leg.wg)
	s.replaceLeg(variantIndex, leg)

	log.Infof("webrtc: media leg for variant %d open (video %s, audio %s)",
		variantIndex, leg.VideoURL(), leg.AudioURL())

	return leg, nil
}

// CloseMediaLeg closes the loopback leg for a variant, if open.
func (s *Service) CloseMediaLeg(variantIndex int) error {
	s.mu.Lock()
	leg := s.legs[variantIndex]
	delete(s.legs, variantIndex)
	s.mu.Unlock()

	if leg == nil {
		return nil
	}

	return leg.Close()
}

func (s *Service) replaceLeg(variantIndex int, leg *MediaLeg) {
	s.mu.Lock()
	previous := s.legs[variantIndex]
	s.legs[variantIndex] = leg
	s.mu.Unlock()

	if previous != nil {
		_ = previous.Close()
	}
}

// VariantIndex returns the variant this leg feeds.
func (l *MediaLeg) VariantIndex() int { return l.variant }

// VideoURL is the FFmpeg RTP destination for the H.264 stream, ready to be
// placed in the transcoder argv next to -f rtp -payload_type 96.
func (l *MediaLeg) VideoURL() string {
	return fmt.Sprintf("rtp://%s:%d?pkt_size=%d", loopbackHost, l.VideoPort(), l.packetSize())
}

// AudioURL is the FFmpeg RTP destination for the Opus stream.
func (l *MediaLeg) AudioURL() string {
	return fmt.Sprintf("rtp://%s:%d?pkt_size=%d", loopbackHost, l.AudioPort(), l.packetSize())
}

func (l *MediaLeg) packetSize() int {
	if l.cfg.RTPPacketSize > 0 {
		return l.cfg.RTPPacketSize
	}

	return minRTCPPayload
}

// VideoPort is the local UDP port FFmpeg writes H.264 RTP to.
func (l *MediaLeg) VideoPort() int { return l.videoConn.LocalAddr().(*net.UDPAddr).Port }

// AudioPort is the local UDP port FFmpeg writes Opus RTP to.
func (l *MediaLeg) AudioPort() int { return l.audioConn.LocalAddr().(*net.UDPAddr).Port }

// MarkRestart tells the forwarders that the next RTP packets belong to a new
// FFmpeg process. The transcoder supervisor should call it before restarting a
// leg; the forwarders also detect restarts heuristically.
func (l *MediaLeg) MarkRestart() {
	l.videoForwarder.markRestart()
	l.audioForwarder.markRestart()
}

// Stats returns the health snapshot of the leg for the admin UI.
func (l *MediaLeg) Stats() MediaLegStats {
	videoPackets, videoBytes, videoParseErrors, videoPTMismatch, videoWriteErrors, videoRebases,
		videoSeq, videoTS, videoLastPacket := l.videoForwarder.stats()
	audioPackets, audioBytes, audioParseErrors, audioPTMismatch, audioWriteErrors, audioRebases,
		audioSeq, audioTS, audioLastPacket := l.audioForwarder.stats()

	sps, pps := l.videoForwarder.parameterSets()
	now := time.Now()

	// Reordered packets are counted across both forwarders.
	reordered := l.videoForwarder.counters.reordered.Load() + l.audioForwarder.counters.reordered.Load()
	measuredFPS := 0.0
	if elapsed := now.Sub(l.videoForwarder.startedAt).Seconds(); elapsed > 1 {
		measuredFPS = float64(l.videoForwarder.counters.frames.Load()) / elapsed
	}

	return MediaLegStats{
		Variant:               l.variant,
		VideoPackets:          videoPackets,
		AudioPackets:          audioPackets,
		VideoBytes:            videoBytes,
		AudioBytes:            audioBytes,
		ParseErrors:           videoParseErrors + audioParseErrors,
		PayloadTypeMismatches: videoPTMismatch + audioPTMismatch,
		WriteErrors:           videoWriteErrors + audioWriteErrors,
		RebaseCount:           videoRebases + audioRebases,
		ReorderedPackets:      reordered,
		MeasuredVideoFPS:      measuredFPS,
		SPSSeen:               len(sps) > 0,
		PPSSeen:               len(pps) > 0,
		LastKeyframeAgeMs:     ageMillis(now, l.videoForwarder.lastIDRAt()),
		LastPacketAgeMs:       ageMillis(now, newest(videoLastPacket, audioLastPacket)),
		VideoLastSequence:     videoSeq,
		VideoLastTimestamp:    videoTS,
		AudioLastSequence:     audioSeq,
		AudioLastTimestamp:    audioTS,
	}
}

// Close closes both sockets (unblocking the forwarders) and waits for them.
func (l *MediaLeg) Close() error {
	l.closeOnce.Do(func() {
		l.service.forgetLeg(l.variant, l)
		_ = l.videoConn.Close()
		_ = l.audioConn.Close()
		l.wg.Wait()
		log.Infof("webrtc: media leg for variant %d closed", l.variant)
	})

	return nil
}

func (s *Service) forgetLeg(variantIndex int, leg *MediaLeg) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.legs[variantIndex] == leg {
		delete(s.legs, variantIndex)
	}
}

func listenLoopbackUDP() (*net.UDPConn, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		return nil, err
	}
	// FFmpeg writes a 1440p60 copy leg at ~1400 packets/s. The default 208 KiB
	// receive buffer is only ~100 ms of media, so a single scheduling hiccup
	// overflows it and the kernel silently drops packets (visible as
	// Udp.RcvbufErrors). Ask for a multi-second buffer; Linux clamps it to
	// net.core.rmem_max.
	if err := conn.SetReadBuffer(loopbackReadBufferBytes); err != nil {
		log.Warnf("webrtc: could not enlarge loopback RTP receive buffer: %v", err)
	}

	return conn, nil
}

func newest(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}

	return b
}

func ageMillis(now, at time.Time) int64 {
	if at.IsZero() || at.UnixNano() == 0 {
		return -1
	}

	return now.Sub(at).Milliseconds()
}
