package rtmp

import (
	"errors"
	"io"
	"net"

	"github.com/teris-io/shortid"

	"github.com/owncast/owncast/services/ingest"
)

// errStreamDisconnectedByAdmin is the close reason used for forced
// admin disconnects. It is intentionally not exposed to viewers.
var errStreamDisconnectedByAdmin = errors.New("inbound stream disconnected by admin")

// session is the ingest.Session implementation for an RTMP/FLV publisher.
type session struct {
	*ingest.SessionBase

	conn   net.Conn
	reader *io.PipeReader
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
		reader: reader,
		writer: writer,
	}
}

// Reader returns the FLV byte stream produced by the RTMP packet pump.
func (s *session) Reader() io.Reader { return s.reader }
