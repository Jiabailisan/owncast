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
	closes  int
	reads   int
	stats   gosrt.Statistics
}

func (c *fakeConn) Read(buffer []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reads++
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
	c.closes++

	return nil
}

// readCount reports how many times Read was called.
func (c *fakeConn) readCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.reads
}

// closeCount reports how many times Close was called.
func (c *fakeConn) closeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.closes
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

// A connection accepted after the session was closed must be refused and closed by
// bind, so the caller can stop instead of leaking it.
func TestBindAfterCloseClosesTheLateConnection(t *testing.T) {
	sess := newSession(nil, "key", 0, false, nil)
	sess.Close(ErrSessionClosed)

	conn := &fakeConn{}
	if sess.bind(conn) {
		t.Fatal("bind must refuse a session that was already closed")
	}
	if !conn.closed {
		t.Fatal("the late connection must be closed by bind")
	}
	if got := conn.closeCount(); got != 1 {
		t.Fatalf("the late connection was closed %d times, want 1", got)
	}
}

// A connection bound before Close is closed exactly once by Close.
func TestCloseAfterBindClosesTheConnectionOnce(t *testing.T) {
	sess := newSession(nil, "key", 0, false, nil)
	conn := &fakeConn{data: []byte("payload")}

	if !sess.bind(conn) {
		t.Fatal("bind must accept a live session")
	}
	sess.Close(ErrRemoteClosed)
	sess.Close(ErrRemoteClosed)

	if !conn.closed {
		t.Fatal("Close must close the bound connection")
	}
	if got := conn.closeCount(); got != 1 {
		t.Fatalf("the connection was closed %d times, want 1", got)
	}
}

// The normal path still reads through the bound connection.
func TestReadUsesTheBoundConnection(t *testing.T) {
	sess := newSession(nil, "key", 0, false, nil)
	conn := &fakeConn{data: []byte("abc")}
	if !sess.bind(conn) {
		t.Fatal("bind must accept a live session")
	}

	buffer := make([]byte, 8)
	n, err := sess.Read(buffer)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buffer[:n]) != "abc" {
		t.Fatalf("read %q, want %q", buffer[:n], "abc")
	}
	if conn.closed {
		t.Fatal("a successful read must not close the connection")
	}
}

// Releasing a finished session must not free the slot a newer session owns.
func TestReleaseOfAnOldSessionKeepsTheNewOwner(t *testing.T) {
	arbiter := ingest.NewArbiter()
	old := newSession(nil, "old", 0, false, nil)
	fresh := newSession(nil, "new", 0, false, nil)

	if err := arbiter.TryAcquire(old); err != nil {
		t.Fatalf("acquire old: %v", err)
	}
	old.Close(ErrRemoteClosed)
	if err := arbiter.TryAcquire(fresh); err != nil {
		t.Fatalf("acquire new: %v", err)
	}

	arbiter.Release(old)

	if current := arbiter.Current(); current == nil || current.ID() != fresh.ID() {
		t.Fatalf("releasing the old session cleared the new owner: %v", current)
	}
}

// streamingFakeConn delivers chunks pushed over a channel and unblocks Read when
// Close is called, mimicking gosrt.Conn for async-drain tests.
type streamingFakeConn struct {
	fakeConn
	ch        chan []byte
	closeCh   chan struct{}
	closeOnce sync.Once
	delivered atomicInt64
}

type atomicInt64 struct {
	mu sync.Mutex
	v  int64
}

func (a *atomicInt64) add(delta int64) {
	a.mu.Lock()
	a.v += delta
	a.mu.Unlock()
}

func (a *atomicInt64) load() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.v
}

func newStreamingFakeConn() *streamingFakeConn {
	return &streamingFakeConn{
		ch:      make(chan []byte),
		closeCh: make(chan struct{}),
	}
}

func (c *streamingFakeConn) Read(buffer []byte) (int, error) {
	select {
	case <-c.closeCh:
		return 0, io.EOF
	case chunk, ok := <-c.ch:
		if !ok {
			return 0, io.EOF
		}
		n := copy(buffer, chunk)
		c.delivered.add(int64(n))
		return n, nil
	}
}

func (c *streamingFakeConn) Close() error {
	c.closeOnce.Do(func() { close(c.closeCh) })
	return c.fakeConn.Close()
}

func TestSessionAsyncDrainBuffersWhileConsumerPaused(t *testing.T) {
	conn := newStreamingFakeConn()
	sess := newBoundTestSession(conn, nil)
	defer sess.Close(ErrRemoteClosed)

	// Send first chunk and read it to kick off the async drain goroutine.
	first := []byte("HELLO-FIRST")
	go func() {
		conn.ch <- first
	}()
	buf := make([]byte, 64)
	n, err := sess.Read(buf)
	if err != nil {
		t.Fatalf("initial Read: %v", err)
	}
	if string(buf[:n]) != string(first) {
		t.Fatalf("initial Read = %q, want %q", buf[:n], first)
	}

	// While the consumer is completely paused, push 64 chunks through the
	// unbuffered channel. Without async draining this would deadlock on chunk 1.
	const chunkCount = 64
	const chunkSize = 1316
	want := make([]byte, 0, chunkCount*chunkSize)
	pushed := make(chan struct{})
	go func() {
		for i := 0; i < chunkCount; i++ {
			chunk := make([]byte, chunkSize)
			for j := range chunk {
				chunk[j] = byte((i + j) % 251)
			}
			want = append(want, chunk...)
			conn.ch <- chunk
		}
		close(conn.ch)
		close(pushed)
	}()

	select {
	case <-pushed:
	case <-time.After(3 * time.Second):
		t.Fatal("async drain did not keep draining conn while consumer was paused")
	}

	// Now resume reading and verify every buffered byte arrives in exact order
	// followed by EOF.
	got := make([]byte, 0, len(want))
	readBuf := make([]byte, 4096)
	for {
		n, err := sess.Read(readBuf)
		if n > 0 {
			got = append(got, readBuf[:n]...)
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("unexpected Read error: %v", err)
		}
	}

	if len(got) != len(want) {
		t.Fatalf("read %d bytes, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("byte mismatch at offset %d: got %d, want %d", i, got[i], want[i])
		}
	}
	if total := sess.PayloadBytes(); total != int64(len(first)+len(want)) {
		t.Fatalf("PayloadBytes = %d, want %d", total, len(first)+len(want))
	}
}

func TestSRTReadBufferCloseUnblocksFullDrain(t *testing.T) {
	conn := newStreamingFakeConn()
	buf := newSRTReadBuffer(conn, 1024, nil)
	buf.start()

	// Fill past maxBytes so the drain goroutine blocks on notFull.Wait().
	go func() {
		for i := 0; i < 4; i++ {
			select {
			case conn.ch <- make([]byte, 1024):
			case <-conn.closeCh:
				return
			}
		}
	}()

	// Wait until chunk 0 is queued (1024 bytes) AND chunk 1 has been read from
	// conn (2048 bytes delivered), proving drainLoop is blocked inside notFull.Wait().
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if conn.delivered.load() >= 2048 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := conn.delivered.load(); got < 2048 {
		t.Fatalf("drainLoop delivered %d bytes, want >= 2048 to reach full buffer", got)
	}

	// Close the buffer BEFORE closing conn so only notFull.Broadcast() can wake
	// drainLoop.
	buf.close()

	select {
	case <-buf.done:
	case <-time.After(2 * time.Second):
		t.Fatal("drain goroutine did not exit after close while buffer was full")
	}
	_ = conn.Close()
}

func TestSRTReadBufferCompactsChunksWithoutDraining(t *testing.T) {
	conn := newStreamingFakeConn()
	buf := newSRTReadBuffer(conn, 1<<20, nil)
	buf.start()
	defer func() {
		buf.close()
		_ = conn.Close()
		<-buf.done
	}()

	// Preload 10 chunks and wait until all 10 are buffered so the queue stays
	// non-empty while we read and push in lockstep, exercising head compaction
	// without ever hitting head == len.
	for i := 0; i < 10; i++ {
		conn.ch <- []byte{byte(i)}
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		buf.mu.Lock()
		queued := buf.buffered
		buf.mu.Unlock()
		if queued >= 10 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	dst := make([]byte, 1)
	for i := 0; i < 600; i++ {
		conn.ch <- []byte{byte(i)}
		if _, err := buf.read(dst); err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
	}

	buf.mu.Lock()
	head := buf.head
	length := len(buf.chunks)
	buf.mu.Unlock()
	if length == 0 || head >= 256 || length > 300 {
		t.Fatalf("chunks failed to compact while non-empty: head=%d len=%d", head, length)
	}
}
