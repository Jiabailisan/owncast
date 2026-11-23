package stream

import (
	"io"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// A WebRTC branch can join a stream that is already running, or restart in the middle of a GOP.
// FFmpeg then receives video packets it cannot decode on its own and floods the log with
// "non-existing PPS" and "no frame!" until the next keyframe arrives. The gate below holds the
// stream back until it can start at a point that decodes: PAT/PMT present, and a keyframe that
// carries its own parameter sets.
const (
	// decodeStartWait bounds how long a branch waits for a decodable start. A stream whose
	// encoder sends no parameter sets at all still has to play, so the gate gives up and
	// passes everything through after this long.
	decodeStartWait = 3 * time.Second
	// decodeStartMaxBytes bounds the memory the gate may hold while it waits.
	decodeStartMaxBytes = 8 << 20
	// tsPacketSize is the MPEG-TS packet size.
	tsPacketSize = 188
)

// decodeStartReader forwards an MPEG-TS stream beginning at a decodable boundary.
//
// One goroutine reads the source and only that goroutine reads it. The gate consumes its results,
// so the wait can expire while a read is blocked and a closed session can release the reader
// instead of leaving it parked inside the source forever.
type decodeStartReader struct {
	done <-chan struct{}

	results chan readChunk

	mu        sync.Mutex
	buf       []byte
	pending   []byte
	ready     bool
	cancelled bool
	skipped   int64
	start     time.Time
}

// readChunk is one result from the reader goroutine.
type readChunk struct {
	data []byte
	err  error
}

func newDecodeStartReader(src io.Reader, done <-chan struct{}) *decodeStartReader {
	r := &decodeStartReader{
		done:    done,
		results: make(chan readChunk, 2),
		start:   time.Now(),
	}

	go r.readLoop(src)

	return r
}

// readLoop is the only reader of src.
func (r *decodeStartReader) readLoop(src io.Reader) {
	defer close(r.results)

	buffer := make([]byte, tsPacketSize*8)
	for {
		n, err := src.Read(buffer)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buffer[:n])
			select {
			case r.results <- readChunk{data: chunk}:
			case <-r.done:
				return
			}
		}
		if err != nil {
			select {
			case r.results <- readChunk{err: err}:
			case <-r.done:
			}

			return
		}
	}
}

// Read forwards the stream, dropping everything before the first decodable boundary.
func (r *decodeStartReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.ready {
		r.fillUntilDecodable()
	}

	if len(r.buf) > 0 {
		n := copy(p, r.buf)
		r.buf = r.buf[n:]

		return n, nil
	}

	if r.cancelled {
		return 0, io.EOF
	}

	// Forward the rest of the stream as it arrives.
	select {
	case chunk, ok := <-r.results:
		if !ok {
			r.cancelled = true

			return 0, io.EOF
		}
		if len(chunk.data) > 0 {
			n := copy(p, chunk.data)
			if n < len(chunk.data) {
				r.buf = append(r.buf, chunk.data[n:]...)
			}

			return n, nil
		}
		r.cancelled = true

		return 0, io.EOF
	case <-r.done:
		r.cancelled = true

		return 0, io.EOF
	}
}

// fillUntilDecodable consumes results until a decodable start is found, the wait expires or the
// session ends. It never blocks on the source itself, so both bounds hold while a read is pending.
func (r *decodeStartReader) fillUntilDecodable() {
	deadline := time.NewTimer(decodeStartWait)
	defer deadline.Stop()

	for {
		select {
		case chunk, ok := <-r.results:
			if !ok {
				r.finish(r.pending)
				r.pending = nil

				return
			}
			if len(chunk.data) > 0 {
				r.pending = append(r.pending, chunk.data...)
				if offset, pat, pmt, found := firstDecodableOffset(r.pending); found {
					r.ready = true
					if offset > 0 {
						r.skipped += int64(offset)
						log.Infof("webrtc decode start: skipped %d bytes to begin at a keyframe with parameter sets", offset)
					}
					r.buf = append(append(append([]byte{}, pat...), pmt...), r.pending[offset:]...)
					r.pending = nil

					return
				}
				if len(r.pending) > decodeStartMaxBytes {
					log.Warnf("webrtc decode start: no decodable boundary within %d bytes; passing the stream through as is", decodeStartMaxBytes)
					r.finish(r.pending)
					r.pending = nil

					return
				}
			}
			if chunk.err != nil {
				r.finish(r.pending)
				r.pending = nil

				return
			}
		case <-deadline.C:
			log.Warnf("webrtc decode start: no decodable boundary within %s; passing the stream through as is", decodeStartWait)
			r.finish(r.pending)
			r.pending = nil

			return
		case <-r.done:
			r.finish(r.pending)
			r.pending = nil

			return
		}
	}
}

// finish hands over the bytes read so far and ends the search.
func (r *decodeStartReader) finish(buffered []byte) {
	r.buf = buffered
	r.ready = true
}

// tsScan is the state carried while looking for a decodable boundary.
type tsScan struct {
	sawPAT, sawPMT bool
	sawSPS, sawPPS bool
	runStart       int
	lastPESStart   int
	prefixEnd      int
	pat, pmt       []byte
}

// firstDecodableOffset finds the offset a decode can safely start at: a PAT and PMT must have
// been seen, and the video stream must have emitted SPS and PPS followed by an IDR. When the run
// already begins right after the program tables nothing is skipped.
func firstDecodableOffset(b []byte) (int, []byte, []byte, bool) {
	scan := &tsScan{runStart: -1, lastPESStart: -1, prefixEnd: -1}

	for off := 0; off+tsPacketSize <= len(b); off += tsPacketSize {
		pkt := b[off : off+tsPacketSize]
		if pkt[0] != 0x47 {
			continue
		}

		if scan.packet(pkt, off) {
			// The keyframe run starts right after the program tables: pass the stream through
			// untouched so the tables, the service description and the timing stay as sent.
			if scan.runStart <= scan.prefixEnd {
				return 0, nil, nil, true
			}

			return scan.runStart, scan.pat, scan.pmt, true
		}
	}

	return 0, nil, nil, false
}

// packet folds one TS packet into the scan and reports whether a decodable run has been found.
func (s *tsScan) packet(pkt []byte, off int) bool {
	pid := int(pkt[1]&0x1f)<<8 | int(pkt[2])

	switch pid {
	case 0:
		s.sawPAT = true
		s.pat = pkt

		return false
	case 0x1000:
		if !s.sawPAT {
			return false
		}
		s.sawPMT = true
		s.pmt = pkt
		s.prefixEnd = off + tsPacketSize

		return false
	}

	// Parameter sets and keyframes only appear in the video elementary stream, so any other PID
	// is a candidate without parsing the PMT.
	pusi := pkt[1]&0x40 != 0
	if (pkt[3]>>4)&0x1 == 0 {
		return false
	}
	if pusi {
		// A keyframe may span several packets, so the run has to begin at the packet that
		// started the PES it lives in, not where the slice happened to appear.
		s.lastPESStart = off
	}

	payload := tsPayload(pkt)
	if hasH264NAL(payload, 7) {
		s.sawSPS = true
		if s.runStart == -1 && s.lastPESStart != -1 {
			s.runStart = s.lastPESStart
		}
	}
	if hasH264NAL(payload, 8) {
		s.sawPPS = true
	}

	return hasH264NAL(payload, 5) && s.sawSPS && s.sawPPS && s.sawPMT && s.runStart != -1
}

// tsPayload returns the payload of a TS packet, or nil when it carries none.
func tsPayload(pkt []byte) []byte {
	afc := (pkt[3] >> 4) & 0x3
	offset := 4
	if afc&0x2 != 0 {
		if offset >= len(pkt) {
			return nil
		}
		offset += 1 + int(pkt[4])
	}
	if afc&0x1 == 0 || offset >= len(pkt) {
		return nil
	}

	return pkt[offset:]
}

// hasH264NAL reports whether the payload contains an H.264 NAL unit of the given type. It scans
// for the start code rather than parsing the PES header, which is enough to spot parameter sets
// and keyframes without depending on the PES layout.
func hasH264NAL(payload []byte, nalType byte) bool {
	for i := 0; i+4 < len(payload); i++ {
		if payload[i] == 0 && payload[i+1] == 0 && payload[i+2] == 1 {
			if payload[i+3]&0x1f == nalType {
				return true
			}
		}
	}

	return false
}
