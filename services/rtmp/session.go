package rtmp

import (
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/teris-io/shortid"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/ingest"
	"github.com/owncast/owncast/services/ingest/flv"
)

// errStreamDisconnectedByAdmin is the close reason used for forced
// admin disconnects. It is intentionally not exposed to viewers.
var errStreamDisconnectedByAdmin = errors.New("inbound stream disconnected by admin")

// countingReader counts media payload bytes for the live-status ingest
// bitrate. RTMP is TCP, so loss/RTT are unavailable (nil).
type countingReader struct {
	reader io.Reader
	bytes  atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.reader.Read(p)
	if n > 0 {
		c.bytes.Add(int64(n))
	}
	return n, err
}

func (c *countingReader) Close() error {
	if closer, ok := c.reader.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// session is the ingest.Session implementation for an RTMP/FLV publisher.
type session struct {
	*ingest.SessionBase

	conn   net.Conn
	reader *countingReader
	writer *io.PipeWriter

	// inspector extracts the H.264 source properties from the FLV tags the
	// packet pump forwards, so RTMP runs the same copy-eligibility rules as SRT.
	inspector *flv.Inspector

	// containerMu guards the cached decoder configuration. FLV sends it once, in
	// sequence-header tags at the start of the stream, so a process that joins
	// later must be handed a rebuilt prefix or it has no extradata at all.
	containerMu sync.Mutex
	videoConfig []byte
	audioConfig []byte

	// sourceMu guards declared, lastSourceKey and publishedBFrames.
	sourceMu sync.Mutex
	// declared is the onMetaData claim, used only where the probe has no value.
	declared      models.InboundStreamDetails
	lastSourceKey string
	// publishedBFrames records the published B-frame verdict; a published "true"
	// is never replaced by an older snapshot.
	publishedBFrames bool

	// sourceReady is closed once the B-frame verdict has been published, so a
	// copy decision does not have to wait for the next statistics tick.
	sourceReady     chan struct{}
	sourceReadyOnce sync.Once
	sourceDecided   atomic.Bool
	// bitratePublished records that the measured video bitrate has been
	// published, so the packet path stops sampling for it.
	bitratePublished atomic.Bool
}

// newSession builds an RTMP session. onClose is called exactly once when the
// session terminates and must release the connection, the pipe and the
// arbiter slot.
func newSession(conn net.Conn, reader *io.PipeReader, writer *io.PipeWriter, keyID string, onClose func(error)) *session {
	return &session{
		SessionBase: ingest.NewSessionBase(
			shortid.MustGenerate(),
			ingest.ProtocolRTMP,
			conn.RemoteAddr(),
			keyID,
			ingest.FLVInputFormat(),
			onClose,
		),
		conn:        conn,
		reader:      &countingReader{reader: reader},
		writer:      writer,
		inspector:   flv.NewInspector(),
		sourceReady: make(chan struct{}),
	}
}

// Reader returns the FLV byte stream produced by the RTMP packet pump.
func (s *session) Reader() io.Reader { return s.reader }

// startStatsSampler publishes the ingest bitrate every second until the
// session ends. TCP cannot report loss/RTT, so those stay nil.
func (s *session) startStatsSampler() {
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		last := s.reader.bytes.Load()
		for {
			select {
			case <-s.Done():
				return
			case <-ticker.C:
				current := s.reader.bytes.Load()
				delta := current - last
				last = current
				kbps := float64(delta*8) / 1000.0
				s.SetStats(ingest.Stats{BitrateKbps: &kbps})
				// The inspector runs on the packet pump, so the tick also
				// publishes properties it discovered since the last one (the
				// bitrate window, for example, needs a second of media time).
				s.refreshSourceMetadata()
			}
		}
	}()
}
