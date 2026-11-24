package stream

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"
)

// incrementingPayload builds a deterministic source so an ordering bug shows up as
// a byte mismatch instead of a timing flake.
func incrementingPayload(size int) []byte {
	payload := make([]byte, size)
	for index := range payload {
		payload[index] = byte(index)
	}

	return payload
}

// finiteReader hands out its payload and then io.EOF, the way an SRT socket behaves
// when the publisher stops. Every read is serialized by the prefetch reader, so the
// position needs no lock of its own.
type finiteReader struct {
	data []byte
	pos  int
}

func (r *finiteReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n

	return n, nil
}

// eofWithDataReader returns its whole payload together with io.EOF, the way a
// socket read can report the final bytes and the end of the stream in one call.
type eofWithDataReader struct {
	data []byte
	sent bool
}

func (r *eofWithDataReader) Read(p []byte) (int, error) {
	if r.sent {
		return 0, io.EOF
	}
	r.sent = true

	return copy(p, r.data), io.EOF
}

// readAllWithinDeadline reads to the end in a goroutine so a reader that never
// returns fails the test instead of hanging it.
func readAllWithinDeadline(t *testing.T, reader io.Reader, deadline time.Duration) ([]byte, error) {
	t.Helper()

	type readResult struct {
		data []byte
		err  error
	}
	done := make(chan readResult, 1)
	go func() {
		data, err := io.ReadAll(reader)
		done <- readResult{data: data, err: err}
	}()

	select {
	case got := <-done:
		return got.data, got.err
	case <-time.After(deadline):
		t.Fatalf("reading the prefetch reader did not finish within %s", deadline)

		return nil, nil
	}
}

// Prefetched bytes are served before the live stream and the buffered chunk is
// never reordered or dropped, so the inspector and ffmpeg see the source verbatim.
func TestPrefetchReaderPreservesOrderAndExitsOnEOF(t *testing.T) {
	t.Run("buffered bytes keep the source order", func(t *testing.T) {
		payload := incrementingPayload(64 * 1024)
		reader := newPrefetchReader(bytes.NewReader(payload))

		reader.Prefetch(time.Now().Add(time.Minute), 4096)

		select {
		case <-reader.BudgetSpent():
		case <-time.After(2 * time.Second):
			t.Fatal("the prefetch goroutine never reported a spent budget")
		}

		got, err := readAllWithinDeadline(t, reader, 2*time.Second)
		if err != nil {
			t.Fatalf("reading the prefetched stream failed: %v", err)
		}
		if len(got) != len(payload) {
			t.Fatalf("read %d bytes, want %d", len(got), len(payload))
		}
		if !bytes.Equal(got, payload) {
			t.Fatal("the prefetched bytes are not byte-for-byte identical to the source")
		}
	})

	t.Run("a final chunk that also reports eof keeps its bytes", func(t *testing.T) {
		payload := incrementingPayload(1024)
		reader := newPrefetchReader(&eofWithDataReader{data: payload})

		// The budget is far larger than the source, so the prefetch ends on the
		// EOF that arrives together with the last bytes.
		reader.Prefetch(time.Now().Add(time.Minute), 1<<20)

		got, err := readAllWithinDeadline(t, reader, 2*time.Second)
		if err != nil {
			t.Fatalf("reading the prefetched stream failed: %v", err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("read %d bytes, want the %d byte source verbatim", len(got), len(payload))
		}
	})

	t.Run("a source that ends stops the prefetch without spending the budget", func(t *testing.T) {
		payload := incrementingPayload(8 * 1024)
		source := &finiteReader{data: payload}
		reader := newPrefetchReader(source)

		// The budget is far larger than the source, so only the EOF can end the
		// prefetch goroutine.
		reader.Prefetch(time.Now().Add(time.Minute), 1<<20)

		got, err := readAllWithinDeadline(t, reader, 2*time.Second)
		if err != nil {
			t.Fatalf("reading the prefetched stream failed: %v", err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("read %d bytes, want the %d byte source verbatim", len(got), len(payload))
		}

		select {
		case <-reader.BudgetSpent():
			t.Fatal("the prefetch reported a spent budget for a source that just ended")
		default:
		}

		// The stream is exhausted: the next read must report io.EOF rather than
		// block or hand out stale bytes.
		probe := make([]byte, 1)
		n, readErr := reader.Read(probe)
		if n != 0 || !errors.Is(readErr, io.EOF) {
			t.Fatalf("reading past the end returned (%d, %v), want (0, io.EOF)", n, readErr)
		}
	})

	// The read-ahead window is a media-time budget, so a source that never
	// reaches the byte ceiling still ends the prefetch at its deadline. A byte
	// budget alone stopped the inspector too early on high-bitrate sources: 1 MiB
	// is about 0.6 s of a 13 Mbps 1440p60 stream, less than the ~1 s the B-frame
	// verdict needs, so every such source lost its copy verdict.
	t.Run("a source below the byte ceiling stops at the deadline", func(t *testing.T) {
		// A live source that keeps delivering: it never reaches the byte
		// ceiling, so only the deadline can end the read-ahead.
		reader := newPrefetchReader(&endlessReader{})

		reader.Prefetch(time.Now().Add(150*time.Millisecond), 1<<20)

		select {
		case <-reader.BudgetSpent():
		case <-time.After(2 * time.Second):
			t.Fatal("the prefetch goroutine never closed the budget at its deadline")
		}

		// The bytes read before the deadline are still served in order.
		probe := make([]byte, 32*1024)
		n, err := reader.Read(probe)
		if err != nil || n == 0 {
			t.Fatalf("reading buffered bytes after the deadline returned (%d, %v)", n, err)
		}
	})
}

// endlessReader is a live source that never ends, so the prefetch can only stop
// on its byte ceiling or its deadline.
type endlessReader struct{}

func (r *endlessReader) Read(p []byte) (int, error) {
	for index := range p {
		p[index] = byte(index)
	}

	return len(p), nil
}
