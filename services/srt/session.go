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

	samplerMu sync.Mutex
	sampler   *statsSampler

	sourceMu      sync.Mutex
	lastSourceKey string

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
	}
	s.lastRead.Store(time.Now().UnixNano())
	return s
}

// bind attaches the accepted gosrt connection. It must be called before any
// reader goroutine starts.
func (s *session) bind(conn gosrt.Conn) {
	s.conn = conn
}

// Reader implements ingest.Session.
func (s *session) Reader() io.Reader {
	return s
}

// Read implements io.Reader over the SRT connection. It counts payload bytes
// (the displayed ingest bitrate is payload-only; gosrt byte counters include
// protocol headers) and closes the session when the connection fails.
func (s *session) Read(buffer []byte) (int, error) {
	if s.conn == nil {
		return 0, ErrSessionNotBound
	}

	n, err := s.conn.Read(buffer)
	if n > 0 {
		s.payload.Add(int64(n))
		s.lastRead.Store(time.Now().UnixNano())
		// Tee the payload into the MPEG-TS/H.264 inspector. Write never blocks
		// and always accepts the bytes, so it cannot affect ingest.
		_, _ = s.inspector.Write(buffer[:n])
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
		s.stopSampler()
		if s.conn != nil {
			_ = s.conn.Close()
		}
		s.SessionBase.Close(reason)
	})
}

// startSampler starts the per-session statistics sampler through the service
// spawn function. It is idempotent.
func (s *session) startSampler(parent context.Context, spawn func(func()) bool, idleTimeout, debugInterval time.Duration, logger *log.Logger) {
	s.samplerMu.Lock()
	defer s.samplerMu.Unlock()
	if s.sampler != nil || s.conn == nil {
		return
	}
	s.sampler = newStatsSampler(
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
	s.sampler.start(parent, spawn)
}

func (s *session) stopSampler() {
	s.samplerMu.Lock()
	sampler := s.sampler
	s.samplerMu.Unlock()
	if sampler != nil {
		sampler.stop()
	}
}

// refreshSourceMetadata publishes the source properties extracted from the
// MPEG-TS stream (codec, resolution, H.264 profile/level, FPS, B-frame
// verdict) as the session broadcaster. It is called by the statistics sampler
// once per tick and only publishes when something changed.
func (s *session) refreshSourceMetadata() {
	if s.inspector == nil {
		return
	}

	info := s.inspector.Snapshot()
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

	s.sourceMu.Lock()
	if key == s.lastSourceKey {
		s.sourceMu.Unlock()
		return
	}
	s.lastSourceKey = key
	s.sourceMu.Unlock()

	details := models.InboundStreamDetails{
		Width:        info.Width,
		Height:       info.Height,
		VideoCodec:   videoCodecDisplayName(info.VideoCodec),
		AudioCodec:   audioCodecDisplayName(info.AudioCodec),
		VideoProfile: info.Profile,
		VideoLevel:   info.Level,
		VideoOnly:    info.AudioCodec == "",

		// Measured media details so the Live Stream Status source panel has
		// the same non-N/A bitrate/audio fields RTMP gets from onMetaData.
		VideoBitrate:    videoBitrate,
		AudioBitrate:    audioBitrate,
		AudioProfile:    info.AudioProfile,
		AudioSampleRate: info.AudioSampleRate,
		AudioChannels:   info.AudioChannels,
	}
	if info.FPSValid {
		details.VideoFramerate = float32(info.FPS)
	}
	if info.BFrameChecked {
		hasBFrames := info.HasBFrames
		details.HasBFrames = &hasBFrames
	}

	s.SetBroadcaster(&models.Broadcaster{
		RemoteAddr:    remoteString(s.RemoteAddr()),
		Time:          time.Now(),
		StreamDetails: details,
	})
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
