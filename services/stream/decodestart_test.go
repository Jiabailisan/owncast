package stream

import (
	"bytes"
	"io"
	"runtime"
	"sync"
	"testing"
	"time"
)

// buildTS assembles an MPEG-TS stream from the given packets so the tests can describe exactly
// what the gate sees.
func buildTS(packets ...[]byte) []byte {
	var out []byte
	for _, p := range packets {
		out = append(out, p...)
	}

	return out
}

// tsCounters gives each PID a running continuity counter, the way a real multiplexer does. A
// fixed counter would look like a discontinuity on every packet.
var (
	tsCounterMu sync.Mutex
	tsCounters  = map[int]int{}
)

func resetTSCounters() {
	tsCounterMu.Lock()
	defer tsCounterMu.Unlock()
	tsCounters = map[int]int{}
}

// tsPacket builds a TS packet with the given PID, payload-unit-start flag and payload.
func tsPacket(pid int, pusi bool, payload []byte) []byte {
	b := make([]byte, tsPacketSize)
	b[0] = 0x47
	b[1] = byte(pid>>8) & 0x1f
	if pusi {
		b[1] |= 0x40
	}
	b[2] = byte(pid)
	tsCounterMu.Lock()
	counter := tsCounters[pid] & 0x0f
	tsCounters[pid] = (counter + 1) & 0x0f
	tsCounterMu.Unlock()
	b[3] = 0x10 | byte(counter) // payload only, running counter
	copy(b[4:], payload)

	return b
}

// patPacket builds a PAT naming the given PMT PID.
func patPacket(pmtPID int) []byte {
	body := []byte{
		0x00, 0xb0, 0x0d, // table id, section length
		0x00, 0x01, 0xc1, 0x00, 0x00, // transport stream id, version, section numbers
		0x00, 0x01, byte(0xe0 | pmtPID>>8), byte(pmtPID), // program 1 -> PMT
		0x00, 0x00, 0x00, 0x00, // CRC placeholder
	}
	payload := append([]byte{0x00}, body...)

	return tsPacket(0, true, payload)
}

// pmtPacket builds a PMT naming the given H.264 video PID.
func pmtPacket(videoPID int) []byte {
	body := []byte{
		0x02, 0xb0, 0x17, // table id, section length
		0x00, 0x01, 0xc1, 0x00, 0x00, // program number, version, section numbers
		0xe0 | 0x00, 0x01, // PCR PID
		0xf0, 0x00, // program info length
		0x1b, byte(0xe0 | videoPID>>8), byte(videoPID), 0xf0, 0x00, // H.264 stream
		0x00, 0x00, 0x00, 0x00, // CRC placeholder
	}
	payload := append([]byte{0x00}, body...)

	return tsPacket(0x1000, true, payload)
}

func TestDecodeStartSkipsToKeyframeWithParameterSets(t *testing.T) {
	sps := []byte{0, 0, 0, 1, 0x67, 0x42, 0x00, 0x1f}
	pps := []byte{0, 0, 0, 1, 0x68, 0xce, 0x3c, 0x80}
	idr := []byte{0, 0, 0, 1, 0x65, 0x88, 0x84}
	nonIDR := []byte{0, 0, 0, 1, 0x41, 0x9a, 0x00}

	stream := buildTS(
		patPacket(0x1000),             // PAT
		pmtPacket(0x100),              // PMT
		tsPacket(0x100, true, nonIDR), // slice before any keyframe
		tsPacket(0x100, true, nonIDR), // another one
		tsPacket(0x100, true, append(append([]byte{}, sps...), pps...)),
		tsPacket(0x100, true, idr),
	)

	r := newDecodeStartReader(bytes.NewReader(stream), make(chan struct{}))
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	// The gate must drop the slices before the parameter sets and start at them.
	if len(got) >= len(stream) {
		t.Fatalf("gate forwarded %d of %d bytes; it should have skipped the leading slices", len(got), len(stream))
	}
	// The gate keeps the program tables and starts the run at the packet carrying the
	// parameter sets, dropping the two slices that preceded it. The expected bytes are taken
	// from the stream itself so the continuity counters match.
	want := append(append([]byte{}, stream[:2*tsPacketSize]...), stream[4*tsPacketSize:]...)
	if !bytes.Equal(got, want) {
		t.Fatalf("gate output %d bytes, want %d (PAT, PMT, parameter sets, IDR)", len(got), len(want))
	}
}

func TestDecodeStartPassesThroughWhenAlreadyDecodable(t *testing.T) {
	sps := []byte{0, 0, 0, 1, 0x67, 0x42}
	pps := []byte{0, 0, 0, 1, 0x68, 0xce}
	idr := []byte{0, 0, 0, 1, 0x65, 0x88}

	stream := buildTS(
		patPacket(0x1000),
		pmtPacket(0x100),
		tsPacket(0x100, true, append(append([]byte{}, sps...), pps...)),
		tsPacket(0x100, true, idr),
	)

	r := newDecodeStartReader(bytes.NewReader(stream), make(chan struct{}))
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, stream) {
		t.Fatalf("a decodable stream must pass through unchanged: got %d bytes, want %d", len(got), len(stream))
	}
}

func TestDecodeStartGivesUpAndPassesThrough(t *testing.T) {
	// No PAT, no parameter sets: the gate cannot find a boundary and must not hold forever.
	stream := buildTS(
		tsPacket(0x100, true, []byte{0, 0, 0, 1, 0x41, 0x9a}),
		tsPacket(0x100, true, []byte{0, 0, 0, 1, 0x41, 0x9b}),
	)

	start := time.Now()
	r := newDecodeStartReader(bytes.NewReader(stream), make(chan struct{}))
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, stream) {
		t.Fatalf("gate must pass the stream through when it gives up: got %d bytes, want %d", len(got), len(stream))
	}
	if time.Since(start) > decodeStartWait+2*time.Second {
		t.Fatalf("gate waited %s, longer than its %s bound", time.Since(start), decodeStartWait)
	}
}

func TestDecodeStartStopsWhenTheSessionEnds(t *testing.T) {
	done := make(chan struct{})
	close(done)

	// A reader that would block forever: the gate must return instead.
	r := newDecodeStartReader(blockingOnlyReader{}, done)
	_, err := r.Read(make([]byte, 64))
	if err != io.EOF {
		t.Fatalf("a cancelled gate must return EOF, got %v", err)
	}
}

type blockingOnlyReader struct{}

func (blockingOnlyReader) Read([]byte) (int, error) {
	select {}
}

// A real OBS stream puts SPS and PPS at the start of a PES and the IDR several packets later,
// inside continuation packets. A gate that only inspects payload-unit starts never sees the
// keyframe, waits out its whole budget and delays every WebRTC start by that much.
func TestDecodeStartFindsAKeyframeInsideAContinuingPES(t *testing.T) {
	spsPPS := []byte{0, 0, 0, 1, 0x67, 0x42, 0, 0, 0, 1, 0x68, 0xce}
	idr := []byte{0, 0, 0, 1, 0x65, 0x88}

	stream := buildTS(
		tsPacket(0x11, true, []byte{0x00, 0x42}), // SDT, as OBS sends
		patPacket(0x1000),
		pmtPacket(0x100),
		tsPacket(0x100, true, spsPPS), // PES starts here
		tsPacket(0x100, false, []byte{0x01, 0x02, 0x03}),
		tsPacket(0x100, false, []byte{0x04, 0x05, 0x06}),
		tsPacket(0x100, false, idr), // keyframe inside a continuation packet
	)

	r := newDecodeStartReader(bytes.NewReader(stream), make(chan struct{}))
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, stream) {
		t.Fatalf("gate dropped %d bytes from an already decodable stream", len(stream)-len(got))
	}
}

// Ending the session must not discard bytes the gate already read. They belong to the stream and
// are delivered before the reader reports the end.
func TestDecodeStartDeliversBufferedBytesOnCancel(t *testing.T) {
	partial := buildTS(tsPacket(0x100, true, []byte{0, 0, 0, 1, 0x41, 0x9a})) // no PAT, no SPS

	done := make(chan struct{})
	src := &cancellingReader{data: partial, done: done}
	r := newDecodeStartReader(src, done)

	// The first read pulls the packet in and finds no boundary; then the session ends.
	var got []byte
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		got = append(got, buf[:n]...)
		if err != nil {
			if err != io.EOF {
				t.Fatalf("unexpected error: %v", err)
			}

			break
		}
	}
	if !bytes.Equal(got, partial) {
		t.Fatalf("gate delivered %d of %d buffered bytes before EOF", len(got), len(partial))
	}
}

// cancellingReader yields its data and then closes the session channel, modelling a publisher
// that disconnects while the gate is still looking for a decodable start.
type cancellingReader struct {
	data []byte
	done chan struct{}
	sent bool
}

func (r *cancellingReader) Read(p []byte) (int, error) {
	if r.sent {
		close(r.done)

		return 0, io.EOF
	}
	r.sent = true
	n := copy(p, r.data)

	return n, nil
}

// blockingSource delivers one chunk and then parks, the way a quiet SRT socket does. The gate's
// wait has to expire while that read is still pending.
type blockingSource struct {
	data []byte
	sent bool
}

func (s *blockingSource) Read(p []byte) (int, error) {
	if !s.sent {
		s.sent = true
		n := copy(p, s.data)

		return n, nil
	}
	select {} // parked forever
}

func TestDecodeStartTimeoutFiresWhileTheReadIsBlocked(t *testing.T) {
	partial := buildTS(tsPacket(0x100, true, []byte{0, 0, 0, 1, 0x41, 0x9a})) // no PAT, no SPS
	r := newDecodeStartReader(&blockingSource{data: partial}, make(chan struct{}))

	start := time.Now()
	var got []byte
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		got = append(got, buf[:n]...)
		if err != nil || len(got) >= len(partial) {
			break
		}
	}

	elapsed := time.Since(start)
	if elapsed > decodeStartWait+2*time.Second {
		t.Fatalf("gate waited %s while the read was blocked, bound is %s", elapsed, decodeStartWait)
	}
	if !bytes.HasPrefix(got, partial) {
		t.Fatalf("gate delivered %d bytes, want the %d bytes it had buffered", len(got), len(partial))
	}
}

func TestDecodeStartCancelReleasesTheBlockedRead(t *testing.T) {
	partial := buildTS(tsPacket(0x100, true, []byte{0, 0, 0, 1, 0x41, 0x9a}))
	done := make(chan struct{})
	r := newDecodeStartReader(&blockingSource{data: partial}, done)

	// The first read pulls the chunk in and blocks waiting for a boundary.
	gotCh := make(chan []byte, 1)
	go func() {
		var got []byte
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			got = append(got, buf[:n]...)
			if err != nil {
				break
			}
		}
		gotCh <- got
	}()

	time.Sleep(50 * time.Millisecond)
	close(done)

	select {
	case got := <-gotCh:
		if !bytes.HasPrefix(got, partial) {
			t.Fatalf("cancel delivered %d bytes, want the %d buffered bytes", len(got), len(partial))
		}
	case <-time.After(decodeStartWait + 3*time.Second):
		t.Fatal("cancel did not release the blocked read")
	}
}

// Removing the WebRTC branch closes its pipe, which is what releases a read parked inside the
// source. The reader goroutine must exit on that, not stay behind for the life of the process.
func TestDecodeStartReaderExitsWhenThePipeCloses(t *testing.T) {
	before := runtime.NumGoroutine()

	pr, pw := io.Pipe()
	done := make(chan struct{})
	r := newDecodeStartReader(pr, done)

	// One chunk so the gate starts scanning, then it parks inside the pipe.
	go func() { _, _ = pw.Write(buildTS(tsPacket(0x100, true, []byte{0, 0, 0, 1, 0x41, 0x9a}))) }()
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := r.Read(buf); err != nil {
				return
			}
		}
	}()

	time.Sleep(50 * time.Millisecond)

	// Closing the pipe is what branch removal does.
	_ = pw.Close()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before+1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	after := runtime.NumGoroutine()
	if after > before+1 {
		t.Fatalf("reader goroutines still running: %d before, %d after", before, after)
	}
}

// A PMT does not have to sit on 0x1000 and the video does not have to sit on 0x100. The gate has
// to follow the program tables rather than assume the usual numbers.
func TestDecodeStartFollowsNonDefaultPMTPID(t *testing.T) {
	resetTSCounters()
	sps := []byte{0, 0, 0, 1, 0x67, 0x42}
	pps := []byte{0, 0, 0, 1, 0x68, 0xce}
	idr := []byte{0, 0, 0, 1, 0x65, 0x88}

	pat := patPacket(0x0abc)
	// Rewrite the PMT packet's PID so it matches the PAT instead of the usual 0x1000.
	pmt := pmtPacket(0x0201)
	pmt[1] = 0x0a // pid high bits
	pmt[2] = 0xbc // pid low byte

	stream := buildTS(
		pat,
		pmt,
		tsPacket(0x0201, true, append(append([]byte{}, sps...), pps...)),
		tsPacket(0x0201, true, idr),
	)

	r := newDecodeStartReader(bytes.NewReader(stream), make(chan struct{}))
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, stream) {
		t.Fatalf("gate dropped %d bytes from a stream with a non-default PMT PID", len(stream)-len(got))
	}
}

// An audio PID interleaved with the video must not disturb the scan.
func TestDecodeStartIgnoresInterleavedAudio(t *testing.T) {
	resetTSCounters()
	sps := []byte{0, 0, 0, 1, 0x67, 0x42}
	pps := []byte{0, 0, 0, 1, 0x68, 0xce}
	idr := []byte{0, 0, 0, 1, 0x65, 0x88}

	// Audio interleaved after the keyframe, so the run is decodable from the start and the
	// audio packets must not be mistaken for video.
	stream := buildTS(
		patPacket(0x1000),
		pmtPacket(0x100),
		tsPacket(0x100, true, append(append([]byte{}, sps...), pps...)),
		tsPacket(0x101, true, []byte{0xff, 0xf1, 0x00}), // audio
		tsPacket(0x100, true, idr),
		tsPacket(0x101, true, []byte{0xff, 0xf1, 0x01}), // audio
	)

	r := newDecodeStartReader(bytes.NewReader(stream), make(chan struct{}))
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, stream) {
		t.Fatalf("interleaved audio disturbed the scan: got %d bytes, want %d", len(got), len(stream))
	}
}

// A continuity jump means packets were lost, so parameter sets from before the break must not be
// paired with a keyframe from after it: the gate keeps waiting instead of starting there.
func TestDecodeStartResetsOnContinuityJump(t *testing.T) {
	resetTSCounters()
	sps := []byte{0, 0, 0, 1, 0x67, 0x42}
	pps := []byte{0, 0, 0, 1, 0x68, 0xce}
	idr := []byte{0, 0, 0, 1, 0x65, 0x88}

	before := tsPacket(0x100, true, append(append([]byte{}, sps...), pps...))
	// Skip a counter value to model a lost packet.
	tsCounterMu.Lock()
	tsCounters[0x100] = (tsCounters[0x100] + 2) & 0x0f
	tsCounterMu.Unlock()
	afterBreak := tsPacket(0x100, true, idr)

	stream := buildTS(patPacket(0x1000), pmtPacket(0x100), before, afterBreak)
	off, _, _, found := firstDecodableOffset(stream)
	if found {
		t.Fatalf("scan accepted a boundary at offset %d across a continuity jump", off)
	}
}

// A start code split across two TS packets must still be recognised.
func TestDecodeStartFindsStartCodeSplitAcrossPackets(t *testing.T) {
	resetTSCounters()
	// The first packet ends with 00 00 and the next begins with 01 67: together an SPS start code.
	head := []byte{0x00, 0x00}
	stream := buildTS(
		patPacket(0x1000),
		pmtPacket(0x100),
		tsPacket(0x100, true, head),
		tsPacket(0x100, false, []byte{0x01, 0x67, 0x42, 0x00, 0x00, 0x00, 0x01, 0x68, 0xce}),
		tsPacket(0x100, false, []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88}),
	)

	off, _, _, found := firstDecodableOffset(stream)
	if !found {
		t.Fatal("scan did not find the boundary when the start code was split across packets")
	}
	// The run starts at the PES that carries the split start code, which is the first packet
	// after the program tables, so nothing is skipped.
	if off != 0 {
		t.Fatalf("boundary at %d, want 0: the run begins right after the program tables", off)
	}
}
