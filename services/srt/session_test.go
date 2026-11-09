package srt

import (
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	gosrt "github.com/datarhei/gosrt"
	"github.com/datarhei/gosrt/packet"

	"github.com/owncast/owncast/services/ingest"
)

// fakeConn implements gosrt.Conn for session unit tests.
type fakeConn struct {
	mu      sync.Mutex
	data    []byte
	readErr error
	closed  bool
	stats   gosrt.Statistics
}

func (c *fakeConn) Read(buffer []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.data) > 0 {
		n := copy(buffer, c.data)
		c.data = c.data[n:]
		return n, nil
	}
	if c.readErr != nil {
		return 0, c.readErr
	}
	return 0, io.EOF
}

func (c *fakeConn) ReadPacket() (packet.Packet, error) { return nil, io.EOF }
func (c *fakeConn) Write(data []byte) (int, error)     { return len(data), nil }
func (c *fakeConn) WritePacket(packet.Packet) error    { return nil }

func (c *fakeConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *fakeConn) LocalAddr() net.Addr { return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1} }

func (c *fakeConn) RemoteAddr() net.Addr { return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 2} }

func (c *fakeConn) SetDeadline(time.Time) error      { return nil }
func (c *fakeConn) SetReadDeadline(time.Time) error  { return nil }
func (c *fakeConn) SetWriteDeadline(time.Time) error { return nil }

func (c *fakeConn) SocketId() uint32     { return 1 }
func (c *fakeConn) PeerSocketId() uint32 { return 2 }
func (c *fakeConn) StreamId() string     { return "testkey" }
func (c *fakeConn) Version() uint32      { return 5 }

func (c *fakeConn) Stats(stats *gosrt.Statistics) {
	if stats != nil {
		*stats = c.stats
	}
}

func newBoundTestSession(conn gosrt.Conn, onClose func(error)) *session {
	sess := newSession(conn.RemoteAddr(), "key:test", 5, false, onClose)
	sess.bind(conn)
	return sess
}

func TestSessionReadCountsPayloadAndPublishesInputFormat(t *testing.T) {
	conn := &fakeConn{data: make([]byte, 26320), readErr: io.EOF}
	closedReason := make(chan error, 1)
	sess := newBoundTestSession(conn, func(reason error) { closedReason <- reason })

	if got := sess.InputFormat().Name; got != "mpegts" {
		t.Errorf("InputFormat = %q, want mpegts", got)
	}
	if got := sess.Protocol(); got != ingest.ProtocolSRT {
		t.Errorf("Protocol = %q, want %q", got, ingest.ProtocolSRT)
	}

	buffer := make([]byte, 4096)
	var total int
	for {
		n, err := sess.Read(buffer)
		total += n
		if err != nil {
			break
		}
	}

	if total != 26320 {
		t.Errorf("read %d bytes, want 26320", total)
	}
	if got := sess.PayloadBytes(); got != int64(total) {
		t.Errorf("PayloadBytes = %d, want %d", got, total)
	}
	if sess.Broadcaster() != nil {
		t.Error("random bytes are not valid MPEG-TS and must not produce broadcaster metadata")
	}

	select {
	case reason := <-closedReason:
		if !errors.Is(reason, ErrRemoteClosed) {
			t.Errorf("close reason = %v, want ErrRemoteClosed", reason)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("session did not close after a read error")
	}

	if !conn.closed {
		t.Error("session close should close the SRT connection")
	}
}

func TestSessionReadBeforeBind(t *testing.T) {
	sess := newSession(&net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}, "key:test", 5, false, nil)
	if _, err := sess.Read(make([]byte, 1)); !errors.Is(err, ErrSessionNotBound) {
		t.Fatalf("Read before bind = %v, want ErrSessionNotBound", err)
	}
	sess.Close(ErrRemoteClosed)
}

func TestSessionCloseIdempotentAndReleaseOnce(t *testing.T) {
	conn := &fakeConn{readErr: io.EOF}
	closeCount := 0
	var mu sync.Mutex
	sess := newBoundTestSession(conn, func(error) {
		mu.Lock()
		closeCount++
		mu.Unlock()
	})

	sess.Close(ErrAdminDisconnect)
	sess.Close(ErrAdminDisconnect)
	if closeCount != 1 {
		t.Errorf("onClose called %d times, want 1", closeCount)
	}
	select {
	case <-sess.Done():
	default:
		t.Error("Done should be closed")
	}
	if sess.Age() < 0 {
		t.Errorf("Age = %v", sess.Age())
	}
}

func TestCodecDisplayNames(t *testing.T) {
	tests := map[string]string{
		"h264":       "H.264",
		"hevc":       "H.265",
		"mpeg2video": "MPEG-2",
		"mpeg4":      "MPEG-4",
		"":           "",
		"something":  "",
	}
	for input, want := range tests {
		if got := videoCodecDisplayName(input); got != want {
			t.Errorf("videoCodecDisplayName(%q) = %q, want %q", input, got, want)
		}
	}

	audioTests := map[string]string{
		"aac":  "AAC",
		"mp3":  "MP3",
		"ac3":  "AC-3",
		"eac3": "E-AC-3",
		"":     "",
	}
	for input, want := range audioTests {
		if got := audioCodecDisplayName(input); got != want {
			t.Errorf("audioCodecDisplayName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSessionRefreshSourceMetadataPopulatesMediaDetails(t *testing.T) {
	fixture := filepath.Join("..", "..", "ingest", "mpegts", "testdata", "sample-720p30-nob-2s.ts")
	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Skipf("fixture not available: %v", err)
	}

	session := newSession(&net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}, "key:test", 5, false, nil)
	defer session.Close(ErrRemoteClosed)

	const chunk = 1000
	for offset := 0; offset < len(data); offset += chunk {
		end := offset + chunk
		if end > len(data) {
			end = len(data)
		}
		if _, err := session.inspector.Write(data[offset:end]); err != nil {
			t.Fatalf("inspector write: %v", err)
		}
	}
	session.refreshSourceMetadata()

	broadcaster := session.Broadcaster()
	if broadcaster == nil {
		t.Fatal("expected broadcaster metadata after inspecting the fixture")
	}
	details := broadcaster.StreamDetails
	if details.VideoBitrate < 1800 || details.VideoBitrate > 4000 {
		t.Errorf("video bitrate = %d, want 1800..4000", details.VideoBitrate)
	}
	if details.AudioBitrate < 100 || details.AudioBitrate > 180 {
		t.Errorf("audio bitrate = %d, want 100..180", details.AudioBitrate)
	}
	if details.AudioProfile != "LC" || details.AudioSampleRate != 48000 || details.AudioChannels != 2 {
		t.Errorf("AAC details = %q %dHz %dch", details.AudioProfile, details.AudioSampleRate, details.AudioChannels)
	}
	if details.VideoCodec != "H.264" || details.AudioCodec != "AAC" {
		t.Errorf("codec labels = %q/%q", details.VideoCodec, details.AudioCodec)
	}
}
