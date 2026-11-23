package stream

import (
	"bytes"
	"io"
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

// tsHeader builds a TS header with the given PID, payload-unit-start flag and payload.
func tsPacket(pid int, pusi bool, payload []byte) []byte {
	b := make([]byte, tsPacketSize)
	b[0] = 0x47
	b[1] = byte(pid>>8) & 0x1f
	if pusi {
		b[1] |= 0x40
	}
	b[2] = byte(pid)
	b[3] = 0x10 // payload only
	copy(b[4:], payload)

	return b
}

func TestDecodeStartSkipsToKeyframeWithParameterSets(t *testing.T) {
	sps := []byte{0, 0, 0, 1, 0x67, 0x42, 0x00, 0x1f}
	pps := []byte{0, 0, 0, 1, 0x68, 0xce, 0x3c, 0x80}
	idr := []byte{0, 0, 0, 1, 0x65, 0x88, 0x84}
	nonIDR := []byte{0, 0, 0, 1, 0x41, 0x9a, 0x00}

	stream := buildTS(
		tsPacket(0, true, []byte{0x00, 0x00, 0xb0}),      // PAT
		tsPacket(0x1000, true, []byte{0x00, 0x02, 0xb0}), // PMT
		tsPacket(0x100, true, nonIDR),                    // slice before any keyframe
		tsPacket(0x100, true, nonIDR),                    // another one
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
	// parameter sets, dropping the slices that preceded it.
	want := buildTS(
		tsPacket(0, true, []byte{0x00, 0x00, 0xb0}),
		tsPacket(0x1000, true, []byte{0x00, 0x02, 0xb0}),
		tsPacket(0x100, true, append(append([]byte{}, sps...), pps...)),
		tsPacket(0x100, true, idr),
	)
	if !bytes.Equal(got, want) {
		t.Fatalf("gate output %d bytes, want %d (PAT, PMT, parameter sets, IDR)", len(got), len(want))
	}
}

func TestDecodeStartPassesThroughWhenAlreadyDecodable(t *testing.T) {
	sps := []byte{0, 0, 0, 1, 0x67, 0x42}
	pps := []byte{0, 0, 0, 1, 0x68, 0xce}
	idr := []byte{0, 0, 0, 1, 0x65, 0x88}

	stream := buildTS(
		tsPacket(0, true, []byte{0x00, 0x00, 0xb0}),
		tsPacket(0x1000, true, []byte{0x00, 0x02, 0xb0}),
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
		tsPacket(0x11, true, []byte{0x00, 0x42}),    // SDT, as OBS sends
		tsPacket(0, true, []byte{0x00, 0x00, 0xb0}), // PAT
		tsPacket(0x1000, true, []byte{0x00, 0x02, 0xb0}),
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
