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
	pmtPID   int
	videoPID int

	sawPAT, sawPMT bool
	sawSPS, sawPPS bool
	runStart       int
	lastPESStart   int
	prefixEnd      int
	pat, pmt       []byte

	// continuity is the last counter seen on the video PID, and ccSeen says whether there is
	// one to compare against.
	continuity  int
	ccSeen      bool
	nalCarry    []byte // tail of the previous payload, for a start code split across packets
	discontSeen int
}

// firstDecodableOffset finds the offset a decode can safely start at: the program tables must name
// an H.264 video stream, and that stream must have emitted SPS and PPS followed by an IDR. When the
// run already begins right after the program tables nothing is skipped.
func firstDecodableOffset(b []byte) (int, []byte, []byte, bool) {
	scan := &tsScan{pmtPID: -1, videoPID: -1, runStart: -1, lastPESStart: -1, prefixEnd: -1}

	for off := 0; off+tsPacketSize <= len(b); off += tsPacketSize {
		pkt := b[off : off+tsPacketSize]
		if pkt[0] != 0x47 {
			continue
		}

		if scan.packet(pkt, off) {
			if scan.runStart <= scan.prefixEnd {
				return 0, nil, nil, true
			}

			return scan.runStart, scan.pat, scan.pmt, true
		}
	}

	return 0, nil, nil, false
}

// packet folds one TS packet into the scan and reports whether a decodable run has been found.
// The branches are the PAT, PMT and video cases plus the payload checks, not independent logic.
//
//nolint:cyclop // one packet classification
func (s *tsScan) packet(pkt []byte, off int) bool {
	pid := int(pkt[1]&0x1f)<<8 | int(pkt[2])

	switch {
	case pid == 0:
		s.sawPAT = true
		s.pat = pkt
		if pmtPID := pmtPIDFromPAT(tsPayload(pkt)); pmtPID >= 0 {
			s.pmtPID = pmtPID
		}

		return false
	case pid == s.pmtPID:
		s.sawPMT = true
		s.pmt = pkt
		s.prefixEnd = off + tsPacketSize
		if videoPID := videoPIDFromPMT(tsPayload(pkt)); videoPID >= 0 {
			s.videoPID = videoPID
		}

		return false
	case pid != s.videoPID:
		return false
	}

	// Only the video elementary stream is tracked from here on.
	pusi := pkt[1]&0x40 != 0
	if (pkt[3]>>4)&0x1 == 0 {
		return false
	}
	if !s.checkContinuity(int(pkt[3] & 0x0f)) {
		return false
	}

	if pusi {
		s.lastPESStart = off
		s.nalCarry = nil
	}

	payload := tsPayload(pkt)
	if len(s.nalCarry) > 0 {
		payload = append(append([]byte{}, s.nalCarry...), payload...)
	}
	// Keep the tail so a start code split across packets is still found.
	if len(payload) > 3 {
		s.nalCarry = append([]byte{}, payload[len(payload)-3:]...)
	}

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

// checkContinuity tracks the video PID's continuity counter and resets the scan state when it
// jumps: parameter sets seen before a break must not be paired with a keyframe after it.
func (s *tsScan) checkContinuity(counter int) bool {
	if s.ccSeen {
		expected := (s.continuity + 1) & 0x0f
		if counter != expected {
			s.discontSeen++
			s.sawSPS = false
			s.sawPPS = false
			s.runStart = -1
			s.nalCarry = nil
		}
	}
	s.continuity = counter
	s.ccSeen = true

	return true
}

// pmtPIDFromPAT reads the first program's PMT PID from a PAT section.
func pmtPIDFromPAT(payload []byte) int {
	section := psiSection(payload)
	if len(section) < 12 {
		return -1
	}
	// 8 bytes of section header, then program entries of 4 bytes each.
	for i := 8; i+4 <= len(section)-4; i += 4 {
		program := int(section[i])<<8 | int(section[i+1])
		if program == 0 {
			continue // network PID, not a program
		}

		return int(section[i+2]&0x1f)<<8 | int(section[i+3])
	}

	return -1
}

// videoPIDFromPMT reads the H.264 video PID from a PMT section.
func videoPIDFromPMT(payload []byte) int {
	section := psiSection(payload)
	if len(section) < 16 {
		return -1
	}
	programInfoLength := int(section[10]&0x0f)<<8 | int(section[11])
	i := 12 + programInfoLength
	for i+5 <= len(section)-4 {
		streamType := section[i]
		elementaryPID := int(section[i+1]&0x1f)<<8 | int(section[i+2])
		esInfoLength := int(section[i+3]&0x0f)<<8 | int(section[i+4])
		if streamType == streamTypeH264 {
			return elementaryPID
		}
		i += 5 + esInfoLength
	}

	return -1
}

// streamTypeH264 is the MPEG-TS stream type for H.264 video.
const streamTypeH264 = 0x1b

// psiSection skips the pointer field and returns the section body.
func psiSection(payload []byte) []byte {
	if len(payload) < 1 {
		return nil
	}
	pointer := int(payload[0])
	if 1+pointer >= len(payload) {
		return nil
	}

	return payload[1+pointer:]
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
