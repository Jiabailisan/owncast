package rtmp

import (
	"errors"
	"io"
	"net"
	"sync/atomic"
	"time"

	"github.com/teris-io/shortid"

	"github.com/owncast/owncast/services/ingest"
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

// session is the ingest.Session implementation for an RTMP/FLV publisher.
type session struct {
	*ingest.SessionBase

	conn   net.Conn
	reader *countingReader
	writer *io.PipeWriter
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
		conn:   conn,
		reader: &countingReader{reader: reader},
		writer: writer,
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
			}
		}
	}()
}
