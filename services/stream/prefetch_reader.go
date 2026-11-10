package stream

import (
	"io"
	"sync"
)

// prefetchReader reads ahead from an ingest reader so the SRT MPEG-TS inspector
// can publish source metadata (which requires consuming media bytes) before the
// transcoder plan fixes the WebRTC copy/transcode decision. Buffered bytes are
// served to FFmpeg first, then it continues reading the live stream.
type prefetchReader struct {
	src io.Reader

	readMu sync.Mutex // serializes reads from src
	mu     sync.Mutex // guards buf
	buf    []byte
}

// newPrefetchReader wraps an ingest reader.
func newPrefetchReader(src io.Reader) *prefetchReader {
	return &prefetchReader{src: src}
}

// Prefetch reads ahead until maxBytes are buffered or the source ends. It runs
// in its own goroutine.
func (p *prefetchReader) Prefetch(maxBytes int) {
	const chunkSize = 32 * 1024
	go func() {
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
				if buffered >= maxBytes {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
}

// Read implements io.Reader: buffered bytes first, then the live stream.
func (p *prefetchReader) Read(b []byte) (int, error) {
	p.mu.Lock()
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
