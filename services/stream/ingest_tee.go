package stream

import (
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"time"

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
	// teeBranchQueueBytes bounds an optional branch by bytes, which is what the memory limit is
	// actually about. Counting chunks alone made the budget depend on how the source delivers:
	// a file-like read fills a 32 KB chunk, while an SRT read is about 1.5 KB, so 256 chunks was
	// 8 MB for one source and 382 KB for the other - about 0.3 s of a 12 Mbps stream, which any
	// brief stall overflowed.
	teeBranchQueueBytes = 4 << 20
	// teeCoalesceWindow is how long the pump may hold small reads to merge them into one chunk.
	// Merging keeps byte order and keeps the chunk count meaningful without adding latency a
	// viewer would notice.
	teeCoalesceWindow = 20 * time.Millisecond
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

	// pending is the number of bytes accepted for this branch but not yet written to its
	// process: queued plus the chunk currently in the pipe write. It is the memory this branch
	// can hold, so it is what the byte budget is checked against.
	pending atomic.Int64

	// Diagnostics, written only by the pump and the fan-out.
	bytesQueued  atomic.Uint64
	bytesOut     atomic.Uint64
	oldestQueued atomic.Int64 // unix nanos when the queue first became non-empty

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
			b.pending.Add(-int64(len(chunk)))
			b.bytesOut.Add(uint64(len(chunk)))
			if len(b.queue) == 0 {
				b.oldestQueued.Store(0)
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

	// Diagnostics: how much the pump has taken from the source, and when it last delivered.
	// They answer whether a branch fell behind because the input arrived in a burst or because
	// nothing was draining it.
	bytesIn  atomic.Uint64
	lastRead atomic.Int64 // unix nanos of the last successful read
}

// newIngestTee wraps the ingest reader.
func newIngestTee(source io.Reader) *ingestTee {
	return &ingestTee{source: source, done: make(chan struct{})}
}

// overBudget reports whether an optional branch would exceed its bounds, and stops it when it
// would. Both bounds apply: bytes, which is the real memory limit, and the chunk count, which
// keeps a flood of tiny objects from being accepted just because it is small in bytes.
func (t *ingestTee) overBudget(branch *teeBranch, next int) bool {
	if branch.pending.Load()+int64(next) <= teeBranchQueueBytes && len(branch.queue) < teeBranchQueueChunks {
		return false
	}

	log.Warnf("ingest tee: branch %q exceeded its byte or chunk budget; stopping it so the critical path continues "+
		"reason=%s queue_depth=%d pending_bytes=%d source_bytes=%d",
		branch.name, branch.overflowReason(next), len(branch.queue),
		branch.pending.Load(), t.bytesIn.Load())
	branch.fail(errTeeBranchOverflow)

	return true
}

// overflowReason names which bound was hit, so the log says why a branch was stopped.
func (b *teeBranch) overflowReason(next int) string {
	if b.pending.Load()+int64(next) > teeBranchQueueBytes {
		return "byte_budget"
	}
	return "chunk_count"
}

// queueBytes reports how much has been queued but not yet written, without underflowing for a
// branch whose enqueue path does not count (the critical branch blocks instead of queueing).
func queueBytes(b *teeBranch) uint64 {
	queued, out := b.bytesQueued.Load(), b.bytesOut.Load()
	if queued <= out {
		return 0
	}
	return queued - out
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
// The loop reads, coalesces, fans out and handles the source ending in one place because they
// share the pending buffer; splitting it would mean passing that buffer and its deadline around.
//
//nolint:cyclop // the branches are the read/flush/EOF cases of one buffer, not independent logic
func (t *ingestTee) Run() {
	t.runOnce.Do(func() {
		buffer := make([]byte, teeReadChunkSize)

		// Low-frequency timeline. Without it the only evidence is the overflow line, which says
		// that a queue was full but not whether the input burst or nothing was draining it.
		statsDone := make(chan struct{})
		defer close(statsDone)
		go func() {
			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()
			lastBytes := uint64(0)
			for {
				select {
				case <-ticker.C:
					in := t.bytesIn.Load()
					delta := in - lastBytes
					lastBytes = in
					t.mu.Lock()
					branches := make([]*teeBranch, len(t.branches))
					copy(branches, t.branches)
					t.mu.Unlock()
					for _, b := range branches {
						queued, out := b.bytesQueued.Load(), b.bytesOut.Load()
						var pending uint64
						if queued > out {
							pending = queued - out
						}
						oldest := int64(0)
						if ts := b.oldestQueued.Load(); ts != 0 {
							oldest = time.Since(time.Unix(0, ts)).Milliseconds()
						}
						log.Infof("ingest tee stats: branch=%s queue_depth=%d queue_bytes=%d "+
							"written_total=%d oldest_queued_ms=%d source_total=%d source_delta_10s=%d",
							b.name, len(b.queue), pending,
							b.bytesOut.Load(), oldest, in, delta)
					}
				case <-statsDone:
					return
				case <-t.done:
					return
				}
			}
		}()

		// Small reads are merged before fan-out. An SRT source delivers roughly 1.5 KB per read,
		// and forwarding each one separately made the chunk-count bound the real limit long
		// before the byte budget was reached. Merging keeps byte order and only delays a chunk
		// by at most teeCoalesceWindow.
		pending := make([]byte, 0, teeReadChunkSize)
		var pendingMu sync.Mutex

		// flush delivers whatever is held for coalescing. It runs from the read loop and from the
		// timer below, because a read can block for a long time and the held bytes must not wait
		// for it: an SRT socket with nothing arriving would otherwise keep a partial chunk here
		// until the next datagram.
		flush := func() {
			pendingMu.Lock()
			if len(pending) == 0 {
				pendingMu.Unlock()

				return
			}
			chunk := make([]byte, len(pending))
			copy(chunk, pending)
			pending = pending[:0]
			pendingMu.Unlock()

			t.fanOut(chunk)
		}

		flushDone := make(chan struct{})
		defer close(flushDone)
		go func() {
			ticker := time.NewTicker(teeCoalesceWindow)
			defer ticker.Stop()

			for {
				select {
				case <-ticker.C:
					flush()
				case <-flushDone:
					return
				case <-t.done:
					return
				}
			}
		}()

		for {
			n, err := t.source.Read(buffer)
			if n > 0 {
				t.bytesIn.Add(uint64(n))
				t.lastRead.Store(time.Now().UnixNano())

				pendingMu.Lock()
				pending = append(pending, buffer[:n]...)
				full := len(pending) >= teeReadChunkSize
				pendingMu.Unlock()

				if full {
					flush()
				}
			}
			// A read can return bytes and a terminal error at the same time. The bytes above
			// are part of the stream, so they are delivered before the branches see the end;
			// the error must not be skipped because the buffer was only partially filled.
			if err != nil {
				if !errors.Is(err, io.EOF) {
					log.Debugln("ingest tee: source ended:", err)
				}
				// Anything still held for coalescing belongs to the stream and must reach the
				// branches before they see EOF.
				flush()
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
		// Both bounds apply: bytes, which is the real memory limit, and the chunk count, which
		// keeps a flood of tiny objects from being accepted just because it is small in bytes.
		if t.overBudget(branch, len(chunk)) {
			continue
		}
		select {
		case branch.queue <- chunk:
			branch.bytesQueued.Add(uint64(len(chunk)))
			branch.pending.Add(int64(len(chunk)))
			branch.oldestQueued.CompareAndSwap(0, time.Now().UnixNano())
		case <-branch.endCh:
		case <-branch.failCh:
		default:
			oldest := int64(0)
			if ts := branch.oldestQueued.Load(); ts != 0 {
				oldest = time.Since(time.Unix(0, ts)).Milliseconds()
			}
			lastReadAgo := int64(-1)
			if ts := t.lastRead.Load(); ts != 0 {
				lastReadAgo = time.Since(time.Unix(0, ts)).Milliseconds()
			}
			log.Warnf("ingest tee: branch %q fell behind; stopping it so the critical path continues "+
				"queue_depth=%d queue_bytes=%d queued_total=%d written_total=%d oldest_queued_ms=%d "+
				"source_bytes=%d source_last_read_ms_ago=%d",
				branch.name, len(branch.queue), queueBytes(branch),
				branch.bytesQueued.Load(), branch.bytesOut.Load(), oldest,
				t.bytesIn.Load(), lastReadAgo)
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
