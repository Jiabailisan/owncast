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
	// teeBranchQueueChunks is a safety net, not the memory bound. It is derived from the byte
	// budget and a 1 KiB minimum chunk so it can never fire before the byte budget does: with the
	// coalescing timer flushing partial chunks, a 256 entry cap fired at about 384 KB of 1.5 KB
	// reads, which is the early stop this bound was meant to avoid.
	teeBranchQueueChunks = teeBranchQueueBytes / 1024
	// teeCriticalBranchQueueChunks bounds the critical (HLS) branch channel so
	// fanOut receives real backpressure once ~1 MiB is queued instead of
	// draining tens of megabytes of prefetched head before FFmpeg starts.
	teeCriticalBranchQueueChunks = 32
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
	// teeCloseTimeout bounds how long Close waits for the sender to flush and finish.
	teeCloseTimeout = 2 * time.Second
	// teeReadBuffer bounds how many read results wait for the owner to merge them.
	teeReadBuffer = 4
)

// errTeeBranchOverflow is the close reason for an optional FFmpeg branch
// that fell behind. The branch's process is stopped; the others continue.
var errTeeBranchOverflow = errors.New("ingest tee branch overflow")

// teeBranch is one FFmpeg stdin pipe fed from the shared ingest reader.
type teeBranch struct {
	name     string
	critical bool

	// aligner, when set, rewrites the first bytes of this branch so it starts at
	// a container boundary. A branch added after the stream started begins in
	// the middle of a container: MPEG-TS only needs the partial head packet
	// dropped, FLV only describes itself at its header.
	aligner any

	reader     *io.PipeReader
	writer     *io.PipeWriter
	queue      chan []byte
	byteBudget int64
	chunkCap   int

	// pending is the number of bytes accepted for this branch but not yet written to its
	// process: queued plus the chunk currently in the pipe write. It is the memory this branch
	// can hold, so it is what the byte budget is checked against.
	pending atomic.Int64

	// Diagnostics, written only by the pump and the fan-out.
	bytesQueued  atomic.Uint64
	bytesOut     atomic.Uint64
	oldestQueued atomic.Int64 // unix nanos when the queue first became non-empty

	onOverflowMu sync.Mutex
	onOverflow   func()

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

// SetOverflow installs the branch's overflow callback after registration. The
// first WebRTC branch is registered before its transcoder exists, so its
// overflow callback is bound afterwards.
func (b *teeBranch) SetOverflow(fn func()) {
	b.onOverflowMu.Lock()
	b.onOverflow = fn
	b.onOverflowMu.Unlock()
}

// fail closes the branch with an error and stops its process (if the overflow
// callback was bound). The callback may be bound after registration, so it is
// read under its mutex.
func (b *teeBranch) fail(err error) {
	b.failOnce.Do(func() {
		close(b.failCh)
		_ = b.writer.CloseWithError(err)
		b.onOverflowMu.Lock()
		fn := b.onOverflow
		b.onOverflowMu.Unlock()
		if fn != nil {
			fn()
		}
	})
}

// finish asks the pump to drain queued chunks and then EOF the branch.
func (b *teeBranch) finish() {
	b.endOnce.Do(func() { close(b.endCh) })
}

// feedAligner passes a chunk through the branch's container aligner, if one is
// still active. It returns the aligned bytes and whether the chunk may be
// written; an aligner that is still searching for the boundary holds the chunk
// back (nothing is written yet).
func (b *teeBranch) feedAligner(chunk []byte) ([]byte, bool) {
	if b.aligner == nil {
		return chunk, true
	}
	var aligned []byte
	var ok bool
	switch a := b.aligner.(type) {
	case *flvAligner:
		aligned, ok = a.feed(chunk)
	case *tsAligner:
		aligned, ok = a.feed(chunk)
	default:
		aligned, ok = chunk, true
	}
	if ok {
		b.aligner = nil
	}

	return aligned, ok
}

// pump writes queued chunks to the branch's pipe. On a graceful finish it
// drains the queue before closing the writer so no tail bytes are lost.
func (b *teeBranch) pump() {
	writeAligned := func(chunk []byte) bool {
		rawLen := int64(len(chunk))
		aligned, ok := b.feedAligner(chunk)
		if !ok {
			b.pending.Add(-rawLen)
			return true
		}
		if _, err := b.writer.Write(aligned); err != nil {
			b.pending.Add(-rawLen)
			b.fail(err)
			return false
		}
		b.pending.Add(-rawLen)
		b.bytesOut.Add(uint64(len(aligned)))
		if len(b.queue) == 0 {
			b.oldestQueued.Store(0)
		}
		return true
	}

	for {
		select {
		case chunk := <-b.queue:
			if !writeAligned(chunk) {
				return
			}
		case <-b.failCh:
			return
		case <-b.endCh:
			for {
				select {
				case chunk := <-b.queue:
					if !writeAligned(chunk) {
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
	stopped   chan struct{}
	closeOnce sync.Once
	runOnce   sync.Once

	// start gates the pump: a caller that registers its branches before Start()
	// gets the whole stream from its first byte, which is what a branch feeding
	// an FFmpeg probe needs (the parameter sets are only in the stream's head).
	startOnce    sync.Once
	startAllowed chan struct{}

	// finished records that the pump stopped feeding branches (source ended,
	// Close ran, or the start gate saw a closed done). Guarded by mu. A branch
	// registered after this gets an already-closed pipe: between finishAll and
	// close(stopped) there is a window where stopped is still open, so the flag,
	// not the channel, is what addBranch must check.
	finished bool

	// Diagnostics: how much the pump has taken from the source, and when it last delivered.
	// They answer whether a branch fell behind because the input arrived in a burst or because
	// nothing was draining it.
	bytesIn  atomic.Uint64
	lastRead atomic.Int64 // unix nanos of the last successful read
}

// newIngestTee wraps the ingest reader. The pump is armed but does not start
// until Start is called.
func newIngestTee(source io.Reader) *ingestTee {
	return &ingestTee{
		source:       source,
		done:         make(chan struct{}),
		stopped:      make(chan struct{}),
		startAllowed: make(chan struct{}),
	}
}

// Start begins reading the source and fanning it out. Every branch registered
// before this call sees the stream from its first byte. The pump runs in its
// own goroutine from here on; Run is kept for callers that manage the goroutine
// themselves and is a no-op after Start.
func (t *ingestTee) Start() {
	t.startOnce.Do(func() {
		close(t.startAllowed)
		go t.Run()
	})
}

// overBudget reports whether an optional branch would exceed its bounds, and stops it when it
// would. Both bounds apply: bytes, which is the real memory limit, and the chunk count, which
// keeps a flood of tiny objects from being accepted just because it is small in bytes.
func (t *ingestTee) overBudget(branch *teeBranch, next int) bool {
	// One snapshot decides and is what the log reports. Reading the counters again afterwards
	// described a different moment, which is how a branch stopped by the byte budget could be
	// logged as a chunk-count stop.
	byteBudget := branch.byteBudget
	if byteBudget <= 0 {
		byteBudget = teeBranchQueueBytes
	}
	chunkCap := branch.chunkCap
	if chunkCap <= 0 {
		chunkCap = teeBranchQueueChunks
	}
	pending := branch.pending.Load()
	depth := len(branch.queue)
	if pending+int64(next) <= byteBudget && depth < chunkCap {
		return false
	}

	reason := "chunk_count"
	if pending+int64(next) > byteBudget {
		reason = "byte_budget"
	}

	log.Warnf("ingest tee: branch %q exceeded its byte or chunk budget; stopping it so the critical path continues "+
		"reason=%s queue_depth=%d pending_bytes=%d next_bytes=%d byte_budget=%d chunk_cap=%d source_bytes=%d",
		branch.name, reason, depth, pending, next, byteBudget, chunkCap, t.bytesIn.Load())
	branch.fail(errTeeBranchOverflow)

	return true
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
	return t.addBranch(name, critical, onOverflow, nil, 0)
}

// AddBranchWithBudget creates a new FFmpeg stdin pipe registered before Start()
// with an optional extra byte budget (capped at webRTCReadAheadMaxBytes) so
// Attempt 0 can absorb a prefetched stream head while FFmpeg spawns without
// running a mid-stream container aligner or injecting duplicate sequence headers.
func (t *ingestTee) AddBranchWithBudget(name string, critical bool, onOverflow func(), extraByteBudget int64) *io.PipeReader {
	return t.addBranch(name, critical, onOverflow, nil, extraByteBudget)
}

// Branch returns the registered branch with the given name, or nil. A caller
// that registered a branch before binding its overflow callback uses this.
func (t *ingestTee) Branch(name string) *teeBranch {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, branch := range t.branches {
		if branch.name == name {
			return branch
		}
	}

	return nil
}

// AddLateBranch adds a branch whose process starts (or restarts) while the
// ingest stream is already flowing, so its first bytes begin mid-container. The
// aligner repairs that: FLV gets a rebuilt header plus the cached decoder
// configuration, MPEG-TS just drops the partial packet at the head.
func (t *ingestTee) AddLateBranch(name string, critical bool, onOverflow func(), container string, prefix []byte) *io.PipeReader {
	return t.AddLateBranchWithBudget(name, critical, onOverflow, container, prefix, 0)
}

// AddLateBranchWithBudget registers a branch with an optional extra byte budget
// (capped at webRTCReadAheadMaxBytes) so Attempt 0 can absorb a prefetched
// stream head while FFmpeg spawns without triggering a spurious overflow.
func (t *ingestTee) AddLateBranchWithBudget(name string, critical bool, onOverflow func(), container string, prefix []byte, extraByteBudget int64) *io.PipeReader {
	var aligner any
	if container == "flv" {
		aligner = newFLVAligner(prefix)
	} else {
		aligner = newTSAligner()
	}

	return t.addBranch(name, critical, onOverflow, aligner, extraByteBudget)
}

func (t *ingestTee) addBranch(name string, critical bool, onOverflow func(), aligner any, extraByteBudget int64) *io.PipeReader {
	// A branch added after the pump finished (the source ended, or Close ran)
	// would sit on a pipe nobody writes to or closes, blocking its reader
	// forever. The check and the registration share t.mu with finishAll, so
	// they cannot interleave: either this branch is in finishAll's snapshot and
	// gets finished, or the flag is already set and it gets a closed pipe.
	t.mu.Lock()
	if t.finished {
		t.mu.Unlock()
		reader, writer := io.Pipe()
		_ = writer.Close()

		return reader
	}

	byteBudget := int64(teeBranchQueueBytes)
	if extraByteBudget > 0 {
		if extraByteBudget > webRTCReadAheadMaxBytes {
			extraByteBudget = webRTCReadAheadMaxBytes
		}
		byteBudget += extraByteBudget
	}
	chunkCap := int(byteBudget / 1024)
	if chunkCap < teeBranchQueueChunks {
		chunkCap = teeBranchQueueChunks
	}
	queueLen := chunkCap
	if critical {
		queueLen = teeCriticalBranchQueueChunks
	}

	reader, writer := io.Pipe()
	branch := &teeBranch{
		name:       name,
		critical:   critical,
		reader:     reader,
		writer:     writer,
		queue:      make(chan []byte, queueLen),
		byteBudget: byteBudget,
		chunkCap:   chunkCap,
		onOverflow: onOverflow,
		aligner:    aligner,
		endCh:      make(chan struct{}),
		failCh:     make(chan struct{}),
	}
	t.branches = append(t.branches, branch)
	t.mu.Unlock()

	go branch.pump()
	log.Tracef("ingest tee: branch %q added (critical=%v aligned=%v byteBudget=%d)", name, critical, aligner != nil, byteBudget)
	return reader
}

// tsAligner repairs a branch that joins an MPEG-TS stream mid-packet. Unlike
// FLV, TS repeats PAT/PMT and sends parameter sets in band, so a late branch
// recovers by itself as soon as it is back on packet boundaries and has seen the
// next keyframe; the repair is to drop the partial packet at the head, which is
// what turns into the garbage bytes FFmpeg reports before "unspecified size".
type tsAligner struct {
	buffered []byte
}

// tsMaxAlignBuffer bounds the head search. A stream that never yields sync
// bytes is forwarded raw rather than buffered forever.
const tsMaxAlignBuffer = 1 << 20

func newTSAligner() *tsAligner { return &tsAligner{} }

// feed buffers the head until it finds two consecutive 188-periodic sync bytes,
// then emits everything from the first of them. Later chunks pass through.
func (a *tsAligner) feed(chunk []byte) ([]byte, bool) {
	a.buffered = append(a.buffered, chunk...)
	offset := tsSyncOffset(a.buffered)
	if offset < 0 {
		if len(a.buffered) >= tsMaxAlignBuffer {
			log.Warnf("ingest tee: no MPEG-TS sync in %d bytes; forwarding the raw bytes", len(a.buffered))
			raw := a.buffered
			a.buffered = nil

			return raw, true
		}

		return nil, false
	}
	aligned := a.buffered[offset:]
	a.buffered = nil

	return aligned, true
}

// tsSyncOffset returns the first offset that starts a run of four consecutive
// 188-periodic sync bytes, or -1. Four packets make a false positive from
// payload filler bytes implausible while needing less than a kilobyte of head.
func tsSyncOffset(buffer []byte) int {
	for offset := 0; offset+188*4 <= len(buffer); offset++ {
		if buffer[offset] != 0x47 {
			continue
		}
		if buffer[offset+188] == 0x47 &&
			buffer[offset+188*2] == 0x47 &&
			buffer[offset+188*3] == 0x47 {
			return offset
		}
	}

	return -1
}

// flvAligner rewrites the first bytes of a mid-stream branch into a valid FLV
// stream: a synthetic file header followed by the first whole tag. It exists
// because a branch that starts in the middle of an FLV tag cannot be demuxed at
// all - unlike MPEG-TS, FLV has no periodic sync pattern.
type flvAligner struct {
	buffered []byte
	// prefix is what the branch is handed before the first whole tag. It is the
	// container header plus the decoder-configuration tags the session cached,
	// because FLV sends that configuration exactly once.
	prefix []byte
}

// flvMaxAlignBuffer bounds how much is buffered while looking for a boundary. A
// source that never yields a plausible tag boundary falls back to the raw bytes
// instead of buffering forever.
const flvMaxAlignBuffer = 4 << 20

// flvFileHeader is a minimal FLV header (audio+video flags, 9-byte data offset)
// followed by the zero PreviousTagSize0 the format requires.
var flvFileHeader = []byte{'F', 'L', 'V', 0x01, 0x05, 0x00, 0x00, 0x00, 0x09, 0x00, 0x00, 0x00, 0x00}

func newFLVAligner(prefix []byte) *flvAligner {
	if len(prefix) == 0 {
		// Without a cached configuration the branch still gets a valid file
		// header, which at least lets the demuxer parse the tags.
		prefix = flvFileHeader
	}

	return &flvAligner{prefix: prefix}
}

// feed buffers a chunk and reports whether the aligned prefix is ready. Once it
// returns true the aligner is finished and the caller passes later chunks
// through untouched.
func (a *flvAligner) feed(chunk []byte) ([]byte, bool) {
	a.buffered = append(a.buffered, chunk...)
	if offset, ok := flvTagBoundary(a.buffered); ok {
		log.Infof("ingest tee: aligned a mid-stream FLV branch at offset %d of %d buffered bytes", offset, len(a.buffered))
		aligned := make([]byte, 0, len(a.prefix)+len(a.buffered)-offset)
		aligned = append(aligned, a.prefix...)
		aligned = append(aligned, a.buffered[offset:]...)

		return aligned, true
	}
	if len(a.buffered) >= flvMaxAlignBuffer {
		log.Warnf("ingest tee: no FLV tag boundary in %d bytes; forwarding the raw bytes", len(a.buffered))
		raw := a.buffered
		a.buffered = nil

		return raw, true
	}

	return nil, false
}

// flvTagBoundary returns the offset of the first position where a valid FLV tag
// header is followed by its matching PreviousTagSize (11 + dataSize) and a
// second valid FLV tag header, or false when none is present yet.
func flvTagBoundary(buffer []byte) (int, bool) {
	for offset := 0; offset+11 <= len(buffer); offset++ {
		if !isFLVTagHeader(buffer[offset:]) {
			continue
		}
		dataSize := flvTagDataSize(buffer[offset:])
		end := offset + 11 + dataSize + 4
		if end+11 > len(buffer) {
			// The next header is not buffered yet, so this candidate cannot be
			// confirmed. Keep looking: a later offset may already be confirmed.
			continue
		}
		prevTagSize := int(buffer[end-4])<<24 | int(buffer[end-3])<<16 | int(buffer[end-2])<<8 | int(buffer[end-1])
		if prevTagSize != 11+dataSize {
			continue
		}
		if isFLVTagHeader(buffer[end:]) {
			return offset, true
		}
	}

	return 0, false
}

// isFLVTagHeader reports whether the 11 bytes look like an FLV tag header.
func isFLVTagHeader(buffer []byte) bool {
	if len(buffer) < 11 {
		return false
	}
	switch buffer[0] {
	case 8, 9, 18:
	default:
		return false
	}
	size := flvTagDataSize(buffer)
	if size <= 0 || size > 16<<20 {
		return false
	}

	return buffer[8] == 0 && buffer[9] == 0 && buffer[10] == 0
}

// flvTagDataSize reads the 24-bit data size of an FLV tag header.
func flvTagDataSize(buffer []byte) int {
	return int(buffer[1])<<16 | int(buffer[2])<<8 | int(buffer[3])
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
		// The pump waits for Start: registering the branches first is what lets a
		// late FFmpeg process receive the stream head (PAT/PMT and the parameter
		// sets) instead of joining mid-packet. A Close that lands before Start
		// must still finish the branches that were already registered, or their
		// readers stay blocked on a pipe nobody closes.
		select {
		case <-t.startAllowed:
		case <-t.done:
			t.finishAll()
			close(t.stopped)

			return
		}
		// Signals Close that the owner has flushed and finished, so Close does not have to
		// finish the branches itself.
		defer close(t.stopped)
		// Small reads are merged before fan-out. An SRT source delivers roughly 1.5 KB per read,
		// and forwarding each one separately made the chunk-count bound the real limit long
		// before the byte budget was reached.
		//
		// One goroutine owns the buffer: it reads results, merges them, flushes on the timer and
		// ends the branches. The reader goroutine only produces, and the timer only wakes the
		// owner up, so no two goroutines ever call fanOut and the byte order is the read order.
		type readResult struct {
			data []byte
			err  error
		}
		results := make(chan readResult, teeReadBuffer)
		readerDone := make(chan struct{})

		go func() {
			defer close(results)
			buffer := make([]byte, teeReadChunkSize)
			for {
				n, err := t.source.Read(buffer)
				if n > 0 {
					t.bytesIn.Add(uint64(n))
					t.lastRead.Store(time.Now().UnixNano())
					chunk := make([]byte, n)
					copy(chunk, buffer[:n])
					select {
					case results <- readResult{data: chunk}:
					case <-t.done:
						return
					}
				}
				if err != nil {
					select {
					case results <- readResult{err: err}:
					case <-t.done:
					}

					return
				}
			}
		}()
		defer close(readerDone)

		pending := make([]byte, 0, teeReadChunkSize)
		flush := func() {
			if len(pending) == 0 {
				return
			}
			chunk := make([]byte, len(pending))
			copy(chunk, pending)
			pending = pending[:0]
			t.fanOut(chunk)
		}

		ticker := time.NewTicker(teeCoalesceWindow)
		defer ticker.Stop()

		for {
			select {
			case result, ok := <-results:
				if !ok {
					flush()
					t.finishAll()

					return
				}
				if len(result.data) > 0 {
					pending = append(pending, result.data...)
					if len(pending) >= teeReadChunkSize {
						flush()
					}
				}
				if result.err != nil {
					// A read can return bytes and a terminal error together. The bytes are
					// merged above, so flushing here delivers the whole tail, in order, exactly
					// once, before the branches see the end.
					if !errors.Is(result.err, io.EOF) {
						log.Debugln("ingest tee: source ended:", result.err)
					}
					flush()
					t.finishAll()

					return
				}
			case <-ticker.C:
				// The timer only wakes the owner; the owner is the only sender.
				flush()
			case <-t.done:
				// Cancellation contract: everything the reader already handed over is
				// delivered before the branches are finished. Results that were queued but not
				// yet merged are drained here, so a close does not silently drop read bytes.
				// Anything the reader produced after this point is discarded, which is what
				// cancelling means.
				for {
					select {
					case result, ok := <-results:
						if !ok {
							flush()
							t.finishAll()

							return
						}
						if len(result.data) > 0 {
							pending = append(pending, result.data...)
						}
					default:
						flush()
						t.finishAll()

						return
					}
				}
			}
		}
	})
}

// Close stops the tee: every branch drains what was already queued, then sees
// EOF. Callers stop the FFmpeg processes themselves.
func (t *ingestTee) Close() {
	t.closeOnce.Do(func() {
		close(t.done)
		// The owner flushes what it still holds and then finishes the branches. Waiting for it
		// keeps the tail of the stream from being dropped: finishing here directly would close
		// the branches while the owner was still merging.
		select {
		case <-t.stopped:
		case <-time.After(teeCloseTimeout):
			log.Warnln("ingest tee: close timed out waiting for the sender; finishing branches")
			t.finishAll()
		}
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
	// The flag is set in the same critical section that snapshots the branches:
	// a branch registering after this either is in the snapshot (and gets
	// finished below) or sees the flag (and gets a closed pipe). Without this,
	// a registration landing between the snapshot and close(stopped) would hang
	// on a pipe nobody writes to or closes.
	t.finished = true
	branches := make([]*teeBranch, len(t.branches))
	copy(branches, t.branches)
	t.mu.Unlock()

	for _, branch := range branches {
		branch.finish()
	}
}
