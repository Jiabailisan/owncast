package ingest

import (
	"io"
	"net"
	"sync"
	"time"

	"github.com/owncast/owncast/models"
)

// Stats is a point-in-time snapshot of protocol-level ingest statistics. Every
// value is nullable because RTMP (TCP) cannot report loss/RTT and some SRT
// counters are not populated by the pinned gosrt version. A nil field must be
// rendered as "N/A", never as zero.
type Stats struct {
	BitrateKbps          *float64
	PacketLossPct        *float64
	RTTMs                *float64
	RetransmittedPackets *int64
	DroppedPackets       *int64
	Extra                map[string]float64
}

// Session is one authenticated inbound media session. The stream service owns
// the live lifecycle and consumes sessions; protocol services (RTMP, SRT)
// implement them. Implementations must be safe for concurrent use: the stream
// service reads Reader once, while status handlers may call Stats concurrently.
type Session interface {
	// ID is a unique session identifier for logs and status correlation.
	ID() string
	// Protocol is the ingest protocol.
	Protocol() Protocol
	// RemoteAddr is the publisher address.
	RemoteAddr() net.Addr
	// StreamKeyID identifies the authenticated stream key: its configured
	// comment when present, otherwise a short non-reversible fingerprint.
	// The raw key is never returned.
	StreamKeyID() string
	// Reader is the live media byte stream fed to ffmpeg's stdin.
	Reader() io.Reader
	// InputFormat describes the container of Reader.
	InputFormat() InputFormat
	// Stats returns the latest protocol-level statistics snapshot.
	Stats() Stats
	// Broadcaster returns source metadata when the protocol carries it
	// (RTMP onMetaData). It is nil for SRT until the MPEG-TS/H.264 inspector
	// fills it in.
	Broadcaster() *models.Broadcaster
	// SetBroadcaster records source metadata (RTMP onMetaData or the SRT
	// MPEG-TS inspector).
	SetBroadcaster(*models.Broadcaster)
	// Close terminates the session. It is idempotent.
	Close(reason error)
	// Age returns how long the session has been established.
	Age() time.Duration
	// Done is closed when the session has terminated.
	Done() <-chan struct{}
}

// SessionBase implements the shared bookkeeping of a Session. Protocol
// adapters embed it and supply the media Reader themselves.
type SessionBase struct {
	id       string
	protocol Protocol
	remote   net.Addr
	keyID    string
	format   InputFormat
	done     chan struct{}

	closeOnce sync.Once
	onClose   func(error)
	startedAt time.Time

	mu          sync.RWMutex
	stats       Stats
	broadcaster *models.Broadcaster
}

// NewSessionBase builds the shared part of a session. onClose (optional) is
// invoked exactly once when the session is closed; it must not block.
func NewSessionBase(id string, protocol Protocol, remote net.Addr, keyID string, format InputFormat, onClose func(error)) *SessionBase {
	return &SessionBase{
		id:       id,
		protocol: protocol,
		remote:   remote,
		keyID:    keyID,
		format:   format,
		done:      make(chan struct{}),
		onClose:   onClose,
		startedAt: time.Now(),
	}
}

// ID implements Session.
func (s *SessionBase) ID() string { return s.id }

// Protocol implements Session.
func (s *SessionBase) Protocol() Protocol { return s.protocol }

// RemoteAddr implements Session.
func (s *SessionBase) RemoteAddr() net.Addr { return s.remote }

// StreamKeyID implements Session.
func (s *SessionBase) StreamKeyID() string { return s.keyID }

// InputFormat implements Session.
func (s *SessionBase) InputFormat() InputFormat { return s.format }

// Stats implements Session.
func (s *SessionBase) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stats
}

// SetStats replaces the statistics snapshot.
func (s *SessionBase) SetStats(stats Stats) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats = stats
}

// Broadcaster implements Session.
func (s *SessionBase) Broadcaster() *models.Broadcaster {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.broadcaster
}

// SetBroadcaster records the source metadata reported by the protocol.
func (s *SessionBase) SetBroadcaster(broadcaster *models.Broadcaster) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.broadcaster = broadcaster
}

// Close implements Session. onClose is invoked exactly once, then Done is
// closed.
func (s *SessionBase) Close(reason error) {
	s.closeOnce.Do(func() {
		if s.onClose != nil {
			s.onClose(reason)
		}
		close(s.done)
	})
}

// Done implements Session.
func (s *SessionBase) Done() <-chan struct{} { return s.done }

// IsClosed reports whether Close has already run.
func (s *SessionBase) IsClosed() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

// Age returns how long the session has existed, for status displays.
func (s *SessionBase) Age() time.Duration {
	return time.Since(s.startedAt)
}
