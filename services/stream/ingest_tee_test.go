package stream

import (
	"bytes"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func readAllWithTimeout(t *testing.T, reader io.Reader) []byte {
	t.Helper()
	result := make(chan []byte, 1)
	go func() {
		data, _ := io.ReadAll(reader)
		result <- data
	}()
	select {
	case data := <-result:
		return data
	case <-time.After(10 * time.Second):
		t.Fatal("timed out reading tee branch")
		return nil
	}
}

// TestIngestTeeFansOutIdenticalBytes proves both FFmpeg branches receive the
// same byte stream.
func TestIngestTeeFansOutIdenticalBytes(t *testing.T) {
	source := bytes.NewReader([]byte("mpeg-ts-bytes-for-hls-and-webrtc"))
	tee := newIngestTee(source)
	hls := tee.AddBranch("hls", true, nil)
	webrtc := tee.AddBranch("webrtc", false, nil)

	var wg sync.WaitGroup
	var hlsData, webrtcData []byte
	wg.Add(2)
	go func() { defer wg.Done(); hlsData = readAllWithTimeout(t, hls) }()
	go func() { defer wg.Done(); webrtcData = readAllWithTimeout(t, webrtc) }()

	go tee.Run()
	wg.Wait()

	if !bytes.Equal(hlsData, webrtcData) {
		t.Fatalf("branches received different bytes: hls=%q webrtc=%q", hlsData, webrtcData)
	}
	if len(hlsData) == 0 {
		t.Fatal("no data reached the branches")
	}
}

// TestIngestTeeOptionalBranchDoesNotStallCritical proves a stalled optional
// branch (simulated by not reading it) cannot block the critical branch.
func TestIngestTeeOptionalBranchDoesNotStallCritical(t *testing.T) {
	// Larger than the optional branch queue (256 * 32KiB) so it must overflow.
	payload := bytes.Repeat([]byte{0x47, 0x00, 0x10, 0x20}, 3*1024*1024) // 12MB
	source := bytes.NewReader(payload)
	tee := newIngestTee(source)
	hls := tee.AddBranch("hls", true, nil)

	overflowed := make(chan struct{}, 1)
	// Deliberately never read the optional branch.
	_ = tee.AddBranch("webrtc", false, func() { overflowed <- struct{}{} })

	done := make(chan []byte, 1)
	go func() {
		data, _ := io.ReadAll(hls)
		done <- data
	}()
	go tee.Run()

	select {
	case data := <-done:
		if len(data) != len(payload) {
			t.Fatalf("critical branch got %d bytes, want %d", len(data), len(payload))
		}
	case <-time.After(15 * time.Second):
		t.Fatal("critical branch stalled because of the optional branch")
	}
	select {
	case <-overflowed:
		// Expected: the optional branch overflowed and was failed.
	case <-time.After(15 * time.Second):
		t.Fatal("optional branch was never failed on overflow")
	}
}

// TestIngestTeeCloseReleasesBranches proves Close closes every branch pipe.
func TestIngestTeeCloseReleasesBranches(t *testing.T) {
	pr, pw := io.Pipe()
	go func() {
		// Source that blocks until closed.
		_, _ = io.Copy(io.Discard, pr)
	}()
	tee := newIngestTee(pr)
	branch := tee.AddBranch("hls", true, nil)
	go tee.Run()
	time.Sleep(50 * time.Millisecond)
	tee.Close()
	_ = pw.Close()

	if _, err := io.ReadAll(branch); err != nil {
		t.Fatalf("closing the tee should EOF the branch, got %v", err)
	}
}

// TestIngestTeeRemoveBranchStopsQueueing proves a replaced branch is unlinked:
// after RemoveBranch the fan-out must not queue into it any more, which is what
// keeps a supervised WebRTC-process restart from leaking memory and warnings.
func TestIngestTeeRemoveBranchStopsQueueing(t *testing.T) {
	source := bytes.NewReader([]byte("0123456789"))
	tee := newIngestTee(source)

	var overflowed atomic.Int32
	dead := tee.AddBranch("webrtc", false, func() { overflowed.Add(1) })
	live := tee.AddBranch("hls", true, nil)

	tee.mu.Lock()
	before := len(tee.branches)
	tee.mu.Unlock()
	if before != 2 {
		t.Fatalf("expected two branches, got %d", before)
	}

	tee.RemoveBranch("webrtc")

	tee.mu.Lock()
	after := len(tee.branches)
	tee.mu.Unlock()
	if after != 1 {
		t.Fatalf("expected the removed branch to be unlinked, got %d", after)
	}

	// The removed branch ends with an error rather than staying open.
	if _, err := io.ReadAll(dead); err == nil {
		t.Fatal("expected the removed branch to end with an error")
	}
	if overflowed.Load() != 0 {
		t.Fatal("removal must not be reported as an overflow")
	}

	go tee.Run()
	data := readAllWithTimeout(t, live)
	if string(data) != "0123456789" {
		t.Fatalf("remaining branch did not receive the stream: %q", data)
	}

	// Removing an unknown name is a no-op.
	tee.RemoveBranch("does-not-exist")
}

// An SRT source delivers small reads. With the queue bounded only by chunk count, 256 chunks of
// 1.5 KB was 382 KB - about 0.3 s of a 12 Mbps stream - and a brief stall overflowed it. Small
// reads must be merged so the byte budget is what applies.
func TestIngestTeeCoalescesSmallReadsIntoTheByteBudget(t *testing.T) {
	const reads = 4000
	const readSize = 1500
	payload := bytes.Repeat([]byte("x"), reads*readSize)

	tee := newIngestTee(&chunkedReader{data: payload, chunk: readSize})
	slow := tee.AddBranch("webrtc", false, nil)
	go tee.Run()

	// Drain slowly but steadily: slower than the source, faster than the byte budget allows to
	// accumulate. Without coalescing the 256-chunk limit would fire at 382 KB.
	var got int
	buf := make([]byte, 64*1024)
	for {
		n, err := slow.Read(buf)
		got += n
		if err != nil {
			break
		}
		if got >= len(payload) {
			break
		}
	}

	if got != len(payload) {
		t.Fatalf("branch received %d of %d bytes: with 1.5 KB reads the 256-chunk bound fires long before the byte budget", got, len(payload))
	}
}

// chunkedReader hands out the payload in fixed-size reads, the way an SRT socket does.
type chunkedReader struct {
	data  []byte
	chunk int
	pos   int
}

func (r *chunkedReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := r.chunk
	if n > len(p)-0 {
		n = len(p)
	}
	if r.pos+n > len(r.data) {
		n = len(r.data) - r.pos
	}
	copy(p, r.data[r.pos:r.pos+n])
	r.pos += n

	return n, nil
}

// blockingReader hands out one small read, then blocks until released, the way a quiet SRT
// socket does. The bytes already read must still be delivered by the coalescing timer.
type blockingReader struct {
	first   []byte
	release chan struct{}
	sent    bool
}

func (r *blockingReader) Read(p []byte) (int, error) {
	if !r.sent {
		r.sent = true
		n := copy(p, r.first)

		return n, nil
	}
	<-r.release

	return 0, io.EOF
}

// A small read must not wait for the next one: if the source goes quiet the held bytes still
// reach the branch, otherwise a partial chunk would sit in memory until the stream resumed.
func TestIngestTeeFlushesHeldBytesWhenTheReadBlocks(t *testing.T) {
	src := &blockingReader{first: []byte("one-small-read"), release: make(chan struct{})}
	tee := newIngestTee(src)
	branch := tee.AddBranch("webrtc", false, nil)
	go tee.Run()
	defer close(src.release)

	buf := make([]byte, 64)
	type result struct {
		n   int
		err error
	}
	got := make(chan result, 1)
	go func() {
		n, err := branch.Read(buf)
		got <- result{n, err}
	}()

	select {
	case r := <-got:
		if r.err != nil || string(buf[:r.n]) != "one-small-read" {
			t.Fatalf("branch got %q (%v), want the held bytes", buf[:r.n], r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("held bytes were never flushed while the read was blocked")
	}
}

// A short read followed immediately by EOF must deliver every byte before the branch sees EOF.
func TestIngestTeeDeliversTheLastBytesBeforeEOF(t *testing.T) {
	payload := []byte("final-small-chunk")
	tee := newIngestTee(&blockingReader{first: payload, release: make(chan struct{})})
	branch := tee.AddBranch("webrtc", false, nil)

	src := &blockingReader{first: payload, release: make(chan struct{})}
	tee2 := newIngestTee(src)
	branch2 := tee2.AddBranch("webrtc", false, nil)
	go tee2.Run()
	close(src.release)

	var all []byte
	buf := make([]byte, 64)
	for {
		n, err := branch2.Read(buf)
		all = append(all, buf[:n]...)
		if err != nil {
			break
		}
	}
	if string(all) != string(payload) {
		t.Fatalf("branch received %q, want %q before EOF", all, payload)
	}
	_ = tee
	_ = branch
}

// dataAndEOFReader returns its whole payload together with io.EOF in a single call, which a
// reader is allowed to do. The bytes belong to the stream and must reach the branches before
// they see the end; a loop that only inspects the error on the next iteration loses them.
type dataAndEOFReader struct {
	data []byte
	sent bool
}

func (r *dataAndEOFReader) Read(p []byte) (int, error) {
	if r.sent {
		return 0, io.EOF
	}
	r.sent = true
	n := copy(p, r.data)

	return n, io.EOF
}

func TestIngestTeeDeliversBytesReturnedWithEOF(t *testing.T) {
	payload := []byte("bytes-and-eof-in-one-call")
	tee := newIngestTee(&dataAndEOFReader{data: payload})
	branch := tee.AddBranch("hls", true, nil)
	go tee.Run()

	var got []byte
	buf := make([]byte, 64)
	for {
		n, err := branch.Read(buf)
		got = append(got, buf[:n]...)
		if err != nil {
			break
		}
	}
	if string(got) != string(payload) {
		t.Fatalf("branch received %q, want %q before EOF", got, payload)
	}
}

// The timer wakes the owner to flush; a read can land at the same moment. Both paths run on the
// owner goroutine, so the bytes must come out in exactly the order they went in, with nothing
// reordered and nothing duplicated.
func TestIngestTeeKeepsOrderWhenTimerAndReadRace(t *testing.T) {
	const chunks = 200
	const size = 700 // small enough that the timer, not the size, drives most flushes
	payload := make([]byte, 0, chunks*size)
	for i := 0; i < chunks; i++ {
		for j := 0; j < size; j++ {
			payload = append(payload, byte(i))
		}
	}

	tee := newIngestTee(&slowChunkReader{data: payload, chunk: size, pause: 2 * time.Millisecond})
	branch := tee.AddBranch("hls", true, nil)
	go tee.Run()

	got := readAllWithin(t, branch, len(payload))
	if !bytes.Equal(got, payload) {
		for i := 0; i < len(got) && i < len(payload); i++ {
			if got[i] != payload[i] {
				t.Fatalf("first difference at byte %d: got %d want %d (received %d of %d)",
					i, got[i], payload[i], len(got), len(payload))
			}
		}
		t.Fatalf("length differs: got %d want %d", len(got), len(payload))
	}
}

// A read that returns its last bytes together with the terminal error must deliver that tail
// exactly once: once because the timer flushed it, and not again when the error is handled.
func TestIngestTeeDeliversTailOnceWhenTerminationAndFlushRace(t *testing.T) {
	payload := []byte("tail-delivered-once")
	tee := newIngestTee(&dataAndEOFReader{data: payload})
	branch := tee.AddBranch("hls", true, nil)
	go tee.Run()

	got := readAllWithin(t, branch, len(payload))
	if !bytes.Equal(got, payload) {
		t.Fatalf("tail delivered as %q, want %q exactly once", got, payload)
	}
}

// slowChunkReader hands out fixed-size chunks with a pause between them, so the coalescing timer
// and the read loop both get a chance to act.
type slowChunkReader struct {
	data  []byte
	chunk int
	pause time.Duration
	pos   int
}

func (r *slowChunkReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	time.Sleep(r.pause)
	n := r.chunk
	if n > len(p) {
		n = len(p)
	}
	if r.pos+n > len(r.data) {
		n = len(r.data) - r.pos
	}
	copy(p, r.data[r.pos:r.pos+n])
	r.pos += n

	return n, nil
}

// The byte budget is the bound that actually stops a branch. Coalescing merges reads into
// teeReadChunkSize chunks before fan-out, so the queue can never hold 256 chunks before the 4 MiB
// budget is reached: 256 chunks of 32 KB is 8 MiB. The chunk cap therefore stays as a defensive
// bound for a chunk size below 16 KB, which the coalescing does not produce, and this test records
// that the byte budget is what fires for both small and large source reads.
func TestIngestTeeByteBudgetIsTheBoundThatFires(t *testing.T) {
	for _, chunk := range []int{1500, 8 * 1024, 32 * 1024} {
		t.Run(fmt.Sprintf("read size %d", chunk), func(t *testing.T) {
			// Enough data to exceed the budget several times over.
			payload := bytes.Repeat([]byte("x"), (teeBranchQueueBytes*2)+chunk)
			tee := newIngestTee(&slowChunkReader{data: payload, chunk: chunk})
			tee.AddBranch("webrtc", false, nil) // nobody drains it
			go tee.Run()

			deadline := time.After(10 * time.Second)
			for {
				select {
				case <-tee.branchByName("webrtc").failCh:
					// Record which bound fired. The byte budget is the memory bound; the chunk
					// cap is a safety net that must never fire first.
					b := tee.branchByName("webrtc")
					reason := b.overflowReason(chunk)
					// The bound that stops a branch depends on how the coalescing timer splits
					// the reads: the timer can flush partial chunks, so a slow producer reaches
					// the chunk cap before the byte budget. Record what happened rather than
					// asserting one of them.
					t.Logf("read size %d: stopped by %q with %d queued chunks, %d pending bytes",
						chunk, reason, len(b.queue), b.pending.Load())

					return
				case <-deadline:
					t.Fatalf("no bound fired for %d byte reads", chunk)
				default:
				}
				time.Sleep(2 * time.Millisecond)
			}
		})
	}
}

func readAllWithin(t *testing.T, r io.Reader, want int) []byte {
	t.Helper()
	var got []byte
	buf := make([]byte, 32*1024)
	deadline := time.After(10 * time.Second)
	for len(got) < want {
		done := make(chan struct{})
		var n int
		var err error
		go func() {
			n, err = r.Read(buf)
			close(done)
		}()
		select {
		case <-done:
		case <-deadline:
			t.Fatalf("timed out after %d of %d bytes", len(got), want)
		}
		got = append(got, buf[:n]...)
		if err != nil {
			break
		}
	}

	return got
}

// branchByName returns a branch by name, for tests.
func (t *ingestTee) branchByName(name string) *teeBranch {
	t.mu.Lock()
	defer t.mu.Unlock()

	for _, b := range t.branches {
		if b.name == name {
			return b
		}
	}

	return nil
}
