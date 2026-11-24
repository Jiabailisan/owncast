package stream

import (
	"io"
	"sync"
	"time"
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
}

// newPrefetchReader wraps an ingest reader.
func newPrefetchReader(src io.Reader) *prefetchReader {
	reader := &prefetchReader{src: src, budgetSpent: make(chan struct{})}
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
		}()
		chunk := make([]byte, chunkSize)
		for {
			p.readMu.Lock()
			n, err := p.src.Read(chunk)
			p.readMu.Unlock()
			if n > 0 {
				p.mu.Lock()
				p.buf = append(p.buf, chunk[:n]...)
				buffered := len(p.buf)
				p.mu.Unlock()
				p.cond.Broadcast()
				if buffered >= maxBytes || time.Now().After(deadline) {
					p.budgetSpentOnce.Do(func() { close(p.budgetSpent) })

					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
}

// BudgetSpent is closed when the prefetch goroutine stopped because the read
// ahead budget was reached. Nothing drains the buffer until the consumer starts,
// so a copy decision still pending at that moment cannot gain new evidence. A
// source that ends or fails stops the prefetch without closing this channel: that
// case is covered by the session's Done channel.
func (p *prefetchReader) BudgetSpent() <-chan struct{} { return p.budgetSpent }

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
		p.mu.Unlock()
		return n, nil
	}
	p.mu.Unlock()

	p.readMu.Lock()
	defer p.readMu.Unlock()
	return p.src.Read(b)
}
