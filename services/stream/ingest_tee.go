package stream

import (
	"errors"
	"io"
	"sync"

	log "github.com/sirupsen/logrus"
)

const (
	// teeReadChunkSize is the size of each chunk copied from the ingest stream
	// to every FFmpeg process in the isolated process model.
	teeReadChunkSize = 32 * 1024
	// teeBranchQueueChunks bounds a non-critical branch's queue. HLS is the
	// critical branch and applies backpressure; the WebRTC branch is optional
	// and is failed (never allowed to stall HLS) if it cannot keep up.
	teeBranchQueueChunks = 256
)

// errTeeBranchOverflow is the close reason for an optional FFmpeg branch
// that fell behind. The branch's process is stopped; the others continue.
var errTeeBranchOverflow = errors.New("ingest tee branch overflow")

// teeBranch is one FFmpeg stdin pipe fed from the shared ingest reader.
type teeBranch struct {
	name     string
	critical bool

	reader *io.PipeReader
	writer *io.PipeWriter
	queue  chan []byte

	onOverflow func()

	endOnce  sync.Once
	endCh    chan struct{}
	failOnce sync.Once
	failCh   chan struct{}
}

// errTeeBranchRemoved ends a branch whose process is being replaced.
var errTeeBranchRemoved = errors.New("ingest tee: branch removed")

// remove ends the branch without reporting an overflow: the branch's process is
// being replaced rather than failing, so onOverflow must not fire.
func (b *teeBranch) remove(err error) {
	b.failOnce.Do(func() {
		close(b.failCh)
		_ = b.writer.CloseWithError(err)
	})
}

// fail closes the branch with an error and stops its process (if configured).
func (b *teeBranch) fail(err error) {
	b.failOnce.Do(func() {
		close(b.failCh)
		_ = b.writer.CloseWithError(err)
		if b.onOverflow != nil {
			b.onOverflow()
		}
	})
}

// finish asks the pump to drain queued chunks and then EOF the branch.
func (b *teeBranch) finish() {
	b.endOnce.Do(func() { close(b.endCh) })
}

// pump writes queued chunks to the branch's pipe. On a graceful finish it
// drains the queue before closing the writer so no tail bytes are lost.
func (b *teeBranch) pump() {
	for {
		select {
		case chunk := <-b.queue:
			if _, err := b.writer.Write(chunk); err != nil {
				b.fail(err)
				return
			}
		case <-b.failCh:
			return
		case <-b.endCh:
			for {
				select {
				case chunk := <-b.queue:
					if _, err := b.writer.Write(chunk); err != nil {
						b.fail(err)
						return
					}
				default:
					_ = b.writer.Close()
					return
				}
			}
		}
	}
}

// ingestTee fans one ingest byte stream out to multiple FFmpeg stdin pipes.
type ingestTee struct {
	source io.Reader

	mu       sync.Mutex
	branches []*teeBranch

	done      chan struct{}
	closeOnce sync.Once
	runOnce   sync.Once
}

// newIngestTee wraps the ingest reader.
func newIngestTee(source io.Reader) *ingestTee {
	return &ingestTee{source: source, done: make(chan struct{})}
}

// AddBranch creates a new FFmpeg stdin pipe. Critical branches block the ingest
// reader (backpressure); optional branches are failed when their bounded queue
// overflows so they can never stall the critical path.
func (t *ingestTee) AddBranch(name string, critical bool, onOverflow func()) *io.PipeReader {
	reader, writer := io.Pipe()
	branch := &teeBranch{
		name:       name,
		critical:   critical,
		reader:     reader,
		writer:     writer,
		queue:      make(chan []byte, teeBranchQueueChunks),
		onOverflow: onOverflow,
		endCh:      make(chan struct{}),
		failCh:     make(chan struct{}),
	}

	t.mu.Lock()
	t.branches = append(t.branches, branch)
	t.mu.Unlock()

	go branch.pump()
	log.Tracef("ingest tee: branch %q added (critical=%v)", name, critical)
	return reader
}

// RemoveBranch finishes and unlinks every branch with the given name. A
// supervised process that restarts calls this before adding its replacement,
// otherwise dead branches would keep queueing chunks and retain memory for the
// rest of the stream.
func (t *ingestTee) RemoveBranch(name string) {
	t.mu.Lock()
	kept := t.branches[:0]
	var removed []*teeBranch
	for _, branch := range t.branches {
		if branch.name == name {
			removed = append(removed, branch)

			continue
		}
		kept = append(kept, branch)
	}
	t.branches = kept
	t.mu.Unlock()

	for _, branch := range removed {
		branch.remove(errTeeBranchRemoved)
		log.Tracef("ingest tee: branch %q removed", name)
	}
}

// Run copies the ingest stream to every branch until it ends or Close is
// called. It is intended to run in its own goroutine.
func (t *ingestTee) Run() {
	t.runOnce.Do(func() {
		buffer := make([]byte, teeReadChunkSize)
		for {
			n, err := t.source.Read(buffer)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buffer[:n])
				t.fanOut(chunk)
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					log.Debugln("ingest tee: source ended:", err)
				}
				t.finishAll()
				return
			}
			select {
			case <-t.done:
				t.finishAll()
				return
			default:
			}
		}
	})
}

// Close stops the tee: every branch drains what was already queued, then sees
// EOF. Callers stop the FFmpeg processes themselves.
func (t *ingestTee) Close() {
	t.closeOnce.Do(func() {
		close(t.done)
		t.finishAll()
	})
}

func (t *ingestTee) fanOut(chunk []byte) {
	t.mu.Lock()
	branches := make([]*teeBranch, len(t.branches))
	copy(branches, t.branches)
	t.mu.Unlock()

	for _, branch := range branches {
		if branch.critical {
			select {
			case branch.queue <- chunk:
			case <-branch.endCh:
			case <-branch.failCh:
			}
			continue
		}
		select {
		case branch.queue <- chunk:
		case <-branch.endCh:
		case <-branch.failCh:
		default:
			log.Warnf("ingest tee: branch %q fell behind; stopping it so the critical path continues", branch.name)
			branch.fail(errTeeBranchOverflow)
		}
	}
}

func (t *ingestTee) finishAll() {
	t.mu.Lock()
	branches := make([]*teeBranch, len(t.branches))
	copy(branches, t.branches)
	t.mu.Unlock()

	for _, branch := range branches {
		branch.finish()
	}
}
