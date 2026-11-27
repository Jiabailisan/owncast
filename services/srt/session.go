package srt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"time"

	gosrt "github.com/datarhei/gosrt"
	log "github.com/sirupsen/logrus"
	"github.com/teris-io/shortid"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/ingest"
	"github.com/owncast/owncast/services/ingest/mpegts"
)

const (
	// srtReadBufferMaxBytes caps the async receive buffer that drains gosrt's
	// fixed 1024-packet (~1.3 MB) non-blocking readQueue. 16 MiB absorbs roughly
	// 10 seconds of 13 Mbps ingest during FFmpeg/CUDA startup pauses without
	// letting gosrt drop packets on backpressure.
	srtReadBufferMaxBytes = 16 << 20
	// srtDrainScratchSize is the reusable buffer passed to gosrt.Conn.Read by
	// the session's async drain goroutine.
	srtDrainScratchSize = 32 * 1024
)

// session is the SRT implementation of ingest.Session. It embeds the shared
// SessionBase bookkeeping and adds the gosrt connection, the payload byte
// counter used for the displayed ingest bitrate, and the statistics sampler.
//
// Once reading starts, a bounded async receive buffer continuously drains the
// gosrt connection so downstream startup stalls (such as FFmpeg CUDA/NVENC init
// or the handoff between prefetch and transcoder) cannot overflow gosrt's
// 1024-packet non-blocking readQueue.
type session struct {
	*ingest.SessionBase

	conn      gosrt.Conn
	version   uint32
	encrypted bool
	startedAt time.Time

	payload   atomic.Int64
	lastRead  atomic.Int64
	inspector *mpegts.Inspector

	sampler *statsSampler
	reader  *srtReadBuffer

	sourceMu      sync.Mutex
	lastSourceKey string

	// sourceReady is closed once the B-frame verdict has been published as the
	// session broadcaster. The stream service selects on it so a copy decision
	// can proceed as soon as the inspector has decided, instead of waiting for
	// the next one-second statistics tick.
	sourceReady     chan struct{}
	sourceReadyOnce sync.Once
	sourceDecided   atomic.Bool
	// publishedBFrames and publishedBFrameVerdict record the B-frame verdict
	// that was published, guarded by sourceMu. A published verdict is never
	// replaced by an older snapshot without one, and "true" is never downgraded
	// back to "false".
	publishedBFrames       bool
	publishedBFrameVerdict bool

	// stateMu guards conn, closed, started, sampler and reader. bind, Close,
	// ensureReadBuffer and the start commit share it, so a session cannot
	// register resources after Close finished and Close can always see what was
	// registered.
	stateMu sync.Mutex
	closed  bool
	started bool

	closeOnce sync.Once
}

// newSession builds an unbound session. onClose is invoked exactly once when
// the session terminates; it must not block.
func newSession(remote net.Addr, keyID string, version uint32, encrypted bool, onClose func(error)) *session {
	id := "srt-" + shortid.MustGenerate()
	base := ingest.NewSessionBase(id, ingest.ProtocolSRT, remote, keyID, ingest.MPEGTSInputFormat(), onClose)
	s := &session{
		SessionBase: base,
		version:     version,
		encrypted:   encrypted,
		startedAt:   time.Now(),
		inspector:   mpegts.NewInspector(),
		sourceReady: make(chan struct{}),
	}
	s.lastRead.Store(time.Now().UnixNano())
	return s
}

// bind attaches the accepted gosrt connection. It must be called before any
// reader goroutine starts. It reports false when the session was already closed, in
// which case the late connection has been closed and the caller must stop.
func (s *session) bind(conn gosrt.Conn) bool {
	s.stateMu.Lock()
	if s.closed {
		s.stateMu.Unlock()
		// Closing the network connection is not done under the lock.
		_ = conn.Close()

		return false
	}
	s.conn = conn
	s.stateMu.Unlock()

	return true
}

// connection returns the bound connection, if any.
func (s *session) connection() gosrt.Conn {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	return s.conn
}

// isClosed reports whether Close already ran.
func (s *session) isClosed() bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	return s.closed
}

// beginStart commits the session to starting. It reports false when Close already
// ran, in which case the caller must not register a sampler, publish an active
// snapshot or invoke the ingest handler.
func (s *session) beginStart() bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	if s.closed {
		return false
	}
	s.started = true

	return true
}

// publishActive runs publish only while the session is still open, under the lock
// Close takes. It reports false when Close already ran, so nothing registered after
// the cleanup survives.
func (s *session) publishActive(publish func()) bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	if s.closed {
		return false
	}
	publish()

	return true
}

// Reader implements ingest.Session.
func (s *session) Reader() io.Reader {
	return s
}

// Read implements io.Reader over the SRT connection via a bounded async drain
// buffer. Payload accounting and MPEG-TS inspection happen as bytes arrive from
// gosrt; session closure on remote EOF/error happens once all buffered bytes
// have been delivered to the caller.
func (s *session) Read(buffer []byte) (int, error) {
	reader, err := s.ensureReadBuffer()
	if err != nil {
		return 0, err
	}

	n, err := reader.read(buffer)
	if err != nil {
		go s.Close(remoteCloseReason(err))
	}

	return n, err
}

func (s *session) ensureReadBuffer() (*srtReadBuffer, error) {
	s.stateMu.Lock()
	if s.conn == nil {
		s.stateMu.Unlock()
		return nil, ErrSessionNotBound
	}
	if s.reader != nil {
		reader := s.reader
		s.stateMu.Unlock()
		return reader, nil
	}
	if s.closed {
		s.stateMu.Unlock()
		return nil, io.EOF
	}
	reader := newSRTReadBuffer(s.conn, srtReadBufferMaxBytes, s.onPayloadRead)
	s.reader = reader
	s.stateMu.Unlock()

	reader.start()
	return reader, nil
}

func (s *session) onPayloadRead(data []byte) {
	s.payload.Add(int64(len(data)))
	s.lastRead.Store(time.Now().UnixNano())
	// Tee the payload into the MPEG-TS/H.264 inspector. Write never blocks
	// and always accepts the bytes, so it cannot affect ingest.
	_, _ = s.inspector.Write(data)
	s.publishSourceInfoIfDecided()
}

// srtReadBuffer continuously drains a gosrt.Conn into a bounded FIFO of exact-
// sized payload chunks so consumer pauses cannot overflow gosrt's small
// non-blocking readQueue.
type srtReadBuffer struct {
	conn     gosrt.Conn
	maxBytes int
	onRead   func([]byte)

	mu         sync.Mutex
	notEmpty   *sync.Cond
	notFull    *sync.Cond
	chunks     [][]byte
	head       int
	headOffset int
	buffered   int
	readErr    error
	closed     bool
	done       chan struct{}
}

func newSRTReadBuffer(conn gosrt.Conn, maxBytes int, onRead func([]byte)) *srtReadBuffer {
	if maxBytes <= 0 {
		maxBytes = srtReadBufferMaxBytes
	}
	b := &srtReadBuffer{
		conn:     conn,
		maxBytes: maxBytes,
		onRead:   onRead,
		done:     make(chan struct{}),
	}
	b.notEmpty = sync.NewCond(&b.mu)
	b.notFull = sync.NewCond(&b.mu)
	return b
}

func (b *srtReadBuffer) start() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		close(b.done)
		return
	}
	b.mu.Unlock()
	go b.drainLoop()
}

func (b *srtReadBuffer) isClosed() bool {
	b.mu.Lock()
	closed := b.closed
	b.mu.Unlock()
	return closed
}

func (b *srtReadBuffer) drainLoop() {
	defer close(b.done)

	scratch := make([]byte, srtDrainScratchSize)
	for {
		if b.isClosed() {
			return
		}
		n, err := b.conn.Read(scratch)
		if n > 0 {
			if b.isClosed() {
				return
			}
			if b.onRead != nil {
				b.onRead(scratch[:n])
			}
			chunk := make([]byte, n)
			copy(chunk, scratch[:n])

			b.mu.Lock()
			for b.buffered >= b.maxBytes && !b.closed {
				b.notFull.Wait()
			}
			if b.closed {
				b.mu.Unlock()
				return
			}
			b.chunks = append(b.chunks, chunk)
			b.buffered += n
			b.notEmpty.Signal()
			b.mu.Unlock()
		}
		if err != nil {
			b.mu.Lock()
			b.readErr = err
			b.notEmpty.Broadcast()
			b.mu.Unlock()
			return
		}
	}
}

func (b *srtReadBuffer) read(dst []byte) (int, error) {
	b.mu.Lock()
	for b.head == len(b.chunks) && b.readErr == nil && !b.closed {
		b.notEmpty.Wait()
	}
	if b.closed {
		b.mu.Unlock()
		return 0, io.EOF
	}
	if b.head < len(b.chunks) {
		if len(dst) == 0 {
			b.mu.Unlock()
			return 0, nil
		}
		head := b.chunks[b.head]
		copied := copy(dst, head[b.headOffset:])
		b.headOffset += copied
		b.buffered -= copied
		if b.headOffset >= len(head) {
			b.chunks[b.head] = nil
			b.head++
			b.headOffset = 0
			if b.head == len(b.chunks) {
				b.head = 0
				if cap(b.chunks) > 256 {
					b.chunks = nil
				} else {
					b.chunks = b.chunks[:0]
				}
			}
		}
		b.notFull.Signal()
		b.mu.Unlock()
		return copied, nil
	}
	err := b.readErr
	b.mu.Unlock()
	if err == nil {
		err = io.EOF
	}
	return 0, err
}

func (b *srtReadBuffer) close() {
	b.mu.Lock()
	if !b.closed {
		b.closed = true
		b.chunks = nil
		b.head = 0
		b.headOffset = 0
		b.buffered = 0
		b.notEmpty.Broadcast()
		b.notFull.Broadcast()
	}
	b.mu.Unlock()
}

func remoteCloseReason(err error) error {
	if err == nil {
		return ErrRemoteClosed
	}
	if errors.Is(err, io.EOF) {
		return ErrRemoteClosed
	}
	return fmt.Errorf("%w: %w", ErrRemoteClosed, err)
}

// Close terminates the session. It stops the statistics sampler, closes the
// SRT connection, and releases the shared bookkeeping (arbiter slot, Done
// channel) exactly once.
func (s *session) Close(reason error) {
	s.closeOnce.Do(func() {
		s.stateMu.Lock()
		s.closed = true
		conn := s.conn
		sampler := s.sampler
		reader := s.reader
		s.sampler = nil
		s.stateMu.Unlock()

		// Stopping the reader/sampler, closing the network connection and running
		// the external callback all happen outside the lock.
		if reader != nil {
			reader.close()
		}
		if sampler != nil {
			sampler.stop()
		}
		if conn != nil {
			_ = conn.Close()
		}
		s.SessionBase.Close(reason)
	})
}

// startSampler starts the per-session statistics sampler through the service
// spawn function. It is idempotent.
func (s *session) startSampler(parent context.Context, spawn func(func()) bool, idleTimeout, debugInterval time.Duration, logger *log.Logger) {
	// Installing the sampler shares the lock Close takes, so either the sampler is
	// registered first and Close stops it, or Close ran first and nothing is started.
	s.stateMu.Lock()
	if s.closed || s.sampler != nil || s.conn == nil {
		s.stateMu.Unlock()

		return
	}
	sampler := newStatsSampler(
		parent,
		s.conn,
		s.SessionBase,
		s.PayloadBytes,
		s.LastReadAt,
		idleTimeout,
		debugInterval,
		logger,
		func(reason error) { s.Close(reason) },
		s.refreshSourceMetadata,
	)
	s.sampler = sampler
	s.stateMu.Unlock()

	sampler.start(spawn)
}

// takeSampler detaches the sampler so the caller can stop it outside the lock.
func (s *session) takeSampler() *statsSampler {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	sampler := s.sampler
	s.sampler = nil

	return sampler
}

func (s *session) stopSampler() {
	if sampler := s.takeSampler(); sampler != nil {
		sampler.stop()
	}
}

// publishSourceInfoIfDecided publishes source metadata from the ingest read
// path as soon as the inspector has enough evidence to settle a WebRTC copy
// decision (non-H.264 codec, confirmed B-frames, or no-B-frames plus SPS
// profile/level/resolution), instead of waiting for the next one-second
// statistics tick. The atomic guard keeps the per-read cost to one load once
// settled.
func (s *session) publishSourceInfoIfDecided() {
	if s.sourceDecided.Load() {
		return
	}
	if !s.inspector.CopyMetadataReady() {
		return
	}
	s.refreshSourceMetadata()
}

// SourceInfoReady is closed once the B-frame verdict has been published as the
// session broadcaster. It stays open for the whole session when the inspector
// never decides (a source that ends mid-GOP, for example).
func (s *session) SourceInfoReady() <-chan struct{} { return s.sourceReady }

// refreshSourceMetadata publishes the source properties extracted from the
// MPEG-TS stream (codec, resolution, H.264 profile/level, FPS, B-frame
// verdict) as the session broadcaster. It is called by the statistics sampler
// once per tick and by the ingest read path when the B-frame verdict lands.
func (s *session) refreshSourceMetadata() {
	if s.inspector == nil {
		return
	}

	s.publishSourceMetadata(s.inspector.Snapshot())
}

// publishSourceMetadata publishes one snapshot of the inspected source
// properties. The statistics sampler and the ingest read path both call it with
// snapshots they took at different moments, so it only publishes a change and it
// never lets a snapshot taken before the B-frame verdict take that verdict away.
func (s *session) publishSourceMetadata(info mpegts.SourceInfo) {
	if info.VideoCodec == "" && info.AudioCodec == "" && info.Width == 0 {
		return
	}

	videoBitrate := 0
	if info.VideoBitrateValid {
		videoBitrate = int(math.Round(info.VideoBitrateKbps))
	}
	audioBitrate := 0
	if info.AudioBitrateValid {
		audioBitrate = int(math.Round(info.AudioBitrateKbps))
	}

	key := fmt.Sprintf("%s|%s|%s|%s|%d|%d|%t|%t|%t|%d|%d|%s|%d|%d",
		info.VideoCodec, info.AudioCodec, info.Profile, info.Level,
		info.Width, info.Height, info.FPSValid, info.HasBFrames, info.BFrameChecked,
		videoBitrate, audioBitrate, info.AudioProfile, info.AudioSampleRate, info.AudioChannels)

	details := models.InboundStreamDetails{
		Width:        info.Width,
		Height:       info.Height,
		VideoCodec:   videoCodecDisplayName(info.VideoCodec),
		AudioCodec:   audioCodecDisplayName(info.AudioCodec),
		VideoProfile: info.Profile,
		VideoLevel:   info.Level,
		VideoOnly:    info.AudioCodec == "",

		// Measured media details so the Live Stream Status source panel has
		// the same non-N/A bitrate/audio fields RTMP gets from onMetaData. The
		// provenance is recorded because the video bitrate here is measured from
		// the PES payload, not declared by the encoder.
		VideoBitrate:       videoBitrate,
		VideoBitrateSource: models.VideoBitrateMeasured,
		AudioBitrate:       audioBitrate,
		AudioProfile:       info.AudioProfile,
		AudioSampleRate:    info.AudioSampleRate,
		AudioChannels:      info.AudioChannels,
	}
	if info.FPSValid {
		details.VideoFramerate = float32(info.FPS)
	}
	if info.BFrameChecked {
		hasBFrames := info.HasBFrames
		details.HasBFrames = &hasBFrames
	}

	// The statistics sampler and the ingest read path both publish, so the change
	// check and the publish share one lock: a snapshot taken before the verdict
	// must not replace the one that carries it.
	s.sourceMu.Lock()
	defer s.sourceMu.Unlock()

	if key == s.lastSourceKey {
		return
	}
	// A published verdict is sticky in the direction that matters. The inspector
	// never revises "uses B-frames" back to "does not", so a snapshot that would
	// take that away was taken earlier and must not replace it: the two publishers
	// can interleave, and a copy decision may already be waking up on the verdict.
	if s.publishedBFrames && (details.HasBFrames == nil || !*details.HasBFrames) {
		return
	}
	// A published "no B-frames" verdict is not replaced by a snapshot that has no
	// verdict at all, for the same reason.
	if s.publishedBFrameVerdict && details.HasBFrames == nil {
		return
	}
	s.lastSourceKey = key

	s.SetBroadcaster(&models.Broadcaster{
		RemoteAddr:    remoteString(s.RemoteAddr()),
		Time:          s.StartedAt(),
		StreamDetails: details,
	})

	// Announce readiness once the copy decision is actually settled: either
	// non-H.264, confirmed B-frames, or no-B-frames paired with SPS metadata
	// (profile, level, resolution). Closing sourceReady earlier on a mid-GOP
	// stream (60 P-frames before the first SPS) would prematurely fall back to
	// transcode with unknown profile/resolution/level.
	if details.HasBFrames != nil {
		s.publishedBFrameVerdict = true
		if *details.HasBFrames {
			s.publishedBFrames = true
		}
	}
	nonH264 := info.VideoCodec != "" && info.VideoCodec != "h264"
	noBFramesWithSPS := details.HasBFrames != nil && !*details.HasBFrames &&
		details.VideoProfile != "" && details.VideoLevel != "" &&
		details.Width > 0 && details.Height > 0
	if nonH264 || (details.HasBFrames != nil && *details.HasBFrames) || noBFramesWithSPS {
		s.sourceReadyOnce.Do(func() { close(s.sourceReady) })
		// Whichever path settled the copy decision, the read path can stop
		// polling the inspector for it.
		s.sourceDecided.Store(true)
	}
}

// videoCodecDisplayName converts the inspector's codec identifiers to the
// display names used by the existing RTMP metadata path.
func videoCodecDisplayName(codec string) string {
	switch codec {
	case "h264":
		return "H.264"
	case "hevc":
		return "H.265"
	case "mpeg1video":
		return "MPEG-1"
	case "mpeg2video":
		return "MPEG-2"
	case "mpeg4":
		return "MPEG-4"
	default:
		return ""
	}
}

func audioCodecDisplayName(codec string) string {
	switch codec {
	case "aac":
		return "AAC"
	case "mp3":
		return "MP3"
	case "ac3":
		return "AC-3"
	case "eac3":
		return "E-AC-3"
	default:
		return ""
	}
}

// PayloadBytes returns the number of payload bytes read from the publisher.
func (s *session) PayloadBytes() int64 {
	return s.payload.Load()
}

// LastReadAt returns the time of the last successful payload read.
func (s *session) LastReadAt() time.Time {
	return time.Unix(0, s.lastRead.Load())
}

// Version returns the negotiated SRT handshake version (4 or 5).
func (s *session) Version() uint32 {
	return s.version
}

// Encrypted reports whether the SRT link is passphrase-encrypted.
func (s *session) Encrypted() bool {
	return s.encrypted
}

// StartedAt returns when the session was created.
func (s *session) StartedAt() time.Time {
	return s.startedAt
}

// compile-time checks.
var (
	_ ingest.Session = (*session)(nil)
)
