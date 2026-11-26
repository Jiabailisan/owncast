package stream

import (
	"io"
	"sync"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
)

// prefetchReader reads ahead from an ingest reader so the SRT MPEG-TS inspector
// can publish source metadata (which requires consuming media bytes) before the
// transcoder plan fixes the WebRTC copy/transcode decision. Buffered bytes are
// served to FFmpeg first, then it continues reading the live stream.
type prefetchReader struct {
	src io.Reader

	readMu sync.Mutex // serializes reads from src
	mu     sync.Mutex // guards buf/prefetching
	cond   *sync.Cond
	buf    []byte

	prefetching bool

	// budgetSpent is closed once the prefetch goroutine stopped because the read
	// ahead budget was reached. From then on no further byte reaches the
	// inspector until a consumer drains the buffer.
	budgetSpent     chan struct{}
	budgetSpentOnce sync.Once

	// stopCh ends the read-ahead early, when the verdict it was feeding already
	// landed: buffering more bytes cannot change the decision and only delays
	// the hand-off to FFmpeg.
	stopOnce sync.Once
	stopCh   chan struct{}

	// Diagnostics for the startup report: how much was read ahead, how long it
	// took, and why it ended.
	startedAt time.Time
	bytesRead atomic.Int64
	endOnce   sync.Once
	endReason string
	stoppedAt time.Time
}

// newPrefetchReader wraps an ingest reader.
func newPrefetchReader(src io.Reader) *prefetchReader {
	reader := &prefetchReader{
		src:         src,
		budgetSpent: make(chan struct{}),
		stopCh:      make(chan struct{}),
		startedAt:   time.Now(),
	}
	reader.cond = sync.NewCond(&reader.mu)

	return reader
}

// Prefetch reads ahead until the deadline passes, maxBytes are buffered or the
// source ends. It runs in its own goroutine.
//
// The read-ahead is bounded by time, not only by bytes. The SRT inspector
// decides the B-frame verdict after a fixed number of slice headers, which is a
// media-time budget (about a second of video), and a byte budget is not: one
// megabyte is roughly 0.6 s of a 13 Mbps 1440p60 source, so the budget was
// exhausted before the inspector could decide and every high-bitrate source
// silently lost its copy verdict. maxBytes remains as a hard memory ceiling for
// unusually high bitrates.
func (p *prefetchReader) Prefetch(deadline time.Time, maxBytes int) {
	p.mu.Lock()
	p.prefetching = true
	p.mu.Unlock()

	const chunkSize = 32 * 1024
	go func() {
		defer func() {
			p.mu.Lock()
			p.prefetching = false
			p.cond.Broadcast()
			p.mu.Unlock()
			p.finish("deadline")
		}()
		chunk := make([]byte, chunkSize)
		for {
			// The verdict may land while this goroutine is parked in a read; check
			// the stop signal before touching the source again.
			select {
			case <-p.stopCh:
				p.budgetSpentOnce.Do(func() { close(p.budgetSpent) })
				p.finish("verdict")

				return
			default:
			}

			p.readMu.Lock()
			n, err := p.src.Read(chunk)
			p.readMu.Unlock()
			if n > 0 {
				p.bytesRead.Add(int64(n))
				p.mu.Lock()
				p.buf = append(p.buf, chunk[:n]...)
				buffered := len(p.buf)
				p.mu.Unlock()
				p.cond.Broadcast()
				if buffered >= maxBytes {
					p.budgetSpentOnce.Do(func() { close(p.budgetSpent) })
					p.finish("memory-ceiling")

					return
				}
			}
			if err != nil {
				p.finish("source-end")

				return
			}
			if time.Now().After(deadline) {
				p.budgetSpentOnce.Do(func() { close(p.budgetSpent) })
				p.finish("deadline")

				return
			}
		}
	}()
}

// Stop ends the read-ahead early. It is called when the copy verdict landed:
// every further buffered byte is dead weight between here and the FFmpeg
// hand-off. The reason is recorded only if the read-ahead has not ended yet.
func (p *prefetchReader) Stop(reason string) {
	p.mu.Lock()
	if p.endReason == "" {
		p.endReason = reason
	}
	p.mu.Unlock()
	p.stopOnce.Do(func() { close(p.stopCh) })
}

// finish records the end of the read-ahead once, with the first reason that
// applied, and logs the startup cost.
func (p *prefetchReader) finish(reason string) {
	p.endOnce.Do(func() {
		p.mu.Lock()
		if p.endReason == "" {
			p.endReason = reason
		}
		p.stoppedAt = time.Now()
		elapsed := p.stoppedAt.Sub(p.startedAt)
		reason = p.endReason
		p.mu.Unlock()

		log.WithFields(map[string]any{
			"bytes":     p.bytesRead.Load(),
			"elapsedMs": elapsed.Milliseconds(),
			"reason":    reason,
		}).Info("webrtc: source read-ahead finished")
	})
}

// Diagnostics returns the read-ahead cost: bytes read, wall-clock duration and
// the reason it ended ("" while still running).
func (p *prefetchReader) Diagnostics() (bytes int64, elapsed time.Duration, reason string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.endReason == "" {
		return p.bytesRead.Load(), time.Since(p.startedAt), ""
	}

	return p.bytesRead.Load(), p.stoppedAt.Sub(p.startedAt), p.endReason
}

// BudgetSpent is closed when the prefetch goroutine stopped because the read
// ahead budget was reached. Nothing drains the buffer until the consumer starts,
// so a copy decision still pending at that moment cannot gain new evidence. A
// source that ends or fails stops the prefetch without closing this channel: that
// case is covered by the session's Done channel.
func (p *prefetchReader) BudgetSpent() <-chan struct{} { return p.budgetSpent }

// BufferedBytes returns the number of unread prefetched bytes currently held
// in memory, including any nested prefetchReader wrapped as src.
func (p *prefetchReader) BufferedBytes() int {
	p.mu.Lock()
	n := len(p.buf)
	p.mu.Unlock()
	if nested, ok := p.src.(*prefetchReader); ok {
		n += nested.BufferedBytes()
	}

	return n
}

func bufferedPrefetchBytes(r io.Reader) int {
	if p, ok := r.(*prefetchReader); ok {
		return p.BufferedBytes()
	}

	return 0
}

// Read implements io.Reader: buffered bytes first, then the live stream.
func (p *prefetchReader) Read(b []byte) (int, error) {
	p.mu.Lock()
	// While the prefetch goroutine is still reading, wait instead of reading the
	// live stream directly: a direct read could return bytes that follow a
	// chunk the prefetcher has read but not yet appended, delivering MPEG-TS
	// data out of order to ffmpeg.
	for len(p.buf) == 0 && p.prefetching {
		p.cond.Wait()
	}
	if len(p.buf) > 0 {
		n := copy(b, p.buf)
		p.buf = p.buf[n:]
		if len(p.buf) == 0 {
			p.buf = nil
		}
		p.mu.Unlock()
		return n, nil
	}
	p.mu.Unlock()

	p.readMu.Lock()
	defer p.readMu.Unlock()
	return p.src.Read(b)
}

// Close stops any active read-ahead and closes the underlying reader when it
// implements io.Closer, unblocking releaseStdinCopy promptly on FFmpeg exit.
func (p *prefetchReader) Close() error {
	p.Stop("closed")
	if closer, ok := p.src.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}
