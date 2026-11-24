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

// session is the SRT implementation of ingest.Session. It embeds the shared
// SessionBase bookkeeping and adds the gosrt connection, the payload byte
// counter used for the displayed ingest bitrate, and the statistics sampler.
//
// The media Reader is the SRT connection itself: gosrt returns one SRT payload
// at a time (up to the negotiated payload size) and MPEG-TS does not require
// read alignment because ffmpeg's mpegts demuxer resynchronizes on the 0x47
// sync byte.
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

	sourceMu      sync.Mutex
	lastSourceKey string

	// sourceReady is closed once the B-frame verdict has been published as the
	// session broadcaster. The stream service selects on it so a copy decision
	// can proceed as soon as the inspector has decided, instead of waiting for
	// the next one-second statistics tick.
	sourceReady     chan struct{}
	sourceReadyOnce sync.Once
	sourceDecided   atomic.Bool
	// publishedBFrames records the B-frame verdict that was published, guarded by
	// sourceMu. A published "true" is never replaced by an older snapshot.
	publishedBFrames bool

	// stateMu guards conn, closed and started. bind, Close and the start commit share
	// it, so a session cannot register resources after Close finished and Close can
	// always see what the start path registered.
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

// Read implements io.Reader over the SRT connection. It counts payload bytes
// (the displayed ingest bitrate is payload-only; gosrt byte counters include
// protocol headers) and closes the session when the connection fails.
func (s *session) Read(buffer []byte) (int, error) {
	conn := s.connection()
	if conn == nil {
		return 0, ErrSessionNotBound
	}

	n, err := conn.Read(buffer)
	if n > 0 {
		s.payload.Add(int64(n))
		s.lastRead.Store(time.Now().UnixNano())
		// Tee the payload into the MPEG-TS/H.264 inspector. Write never blocks
		// and always accepts the bytes, so it cannot affect ingest.
		_, _ = s.inspector.Write(buffer[:n])
		s.publishSourceInfoIfDecided()
	}
	if err != nil {
		go s.Close(remoteCloseReason(err))
	}

	return n, err
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
		s.sampler = nil
		s.stateMu.Unlock()

		// Stopping the sampler, closing the network connection and running the
		// external callback all happen outside the lock.
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

// publishSourceInfoIfDecided publishes the B-frame verdict from the ingest read
// path as soon as the inspector reaches it, instead of leaving it to the next
// one-second statistics tick. A copy decision that needs the verdict therefore
// does not wait for the sampler. The atomic guard keeps the per-read cost to one
// load once the verdict is out.
func (s *session) publishSourceInfoIfDecided() {
	if s.sourceDecided.Load() {
		return
	}
	if !s.inspector.BFrameDecided() {
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
	if s.sourceDecided.Load() && details.HasBFrames == nil {
		return
	}
	s.lastSourceKey = key

	s.SetBroadcaster(&models.Broadcaster{
		RemoteAddr:    remoteString(s.RemoteAddr()),
		Time:          time.Now(),
		StreamDetails: details,
	})

	// The verdict is what a copy decision waits for: announce it exactly once,
	// whichever path published it (read path or statistics tick).
	if details.HasBFrames != nil {
		s.publishedBFrames = *details.HasBFrames
		s.sourceReadyOnce.Do(func() { close(s.sourceReady) })
		// Whichever path published the verdict, the read path can stop polling
		// the inspector for it.
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
