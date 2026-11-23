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
type decodeStartReader struct {
	src  io.Reader
	done <-chan struct{}

	mu      sync.Mutex
	buf     []byte
	ready   bool
	skipped int64
	gaveUp  bool
	start   time.Time
	pending []byte // leftover bytes carried between Reads
}

func newDecodeStartReader(src io.Reader, done <-chan struct{}) *decodeStartReader {
	return &decodeStartReader{src: src, done: done, start: time.Now()}
}

// Read forwards the stream, dropping everything before the first decodable boundary.
func (r *decodeStartReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.ready {
		if err := r.fillUntilDecodable(p); err != nil {
			return 0, err
		}
	}

	// Serve what the boundary search already buffered.
	if len(r.buf) > 0 {
		n := copy(p, r.buf)
		r.buf = r.buf[n:]

		return n, nil
	}

	return r.src.Read(p)
}

// fillUntilDecodable reads until a decodable start is found, the wait expires or the buffer cap
// is reached. When it returns, r.buf holds the bytes to deliver from the boundary onward.
func (r *decodeStartReader) fillUntilDecodable(p []byte) error {
	for {
		select {
		case <-r.done:
			return io.EOF
		default:
		}

		n, err := r.src.Read(p)
		if n > 0 {
			r.pending = append(r.pending, p[:n]...)
			if offset, pat, pmt, ok := firstDecodableOffset(r.pending); ok {
				r.buf = append(append(append([]byte{}, pat...), pmt...), r.pending[offset:]...)
				r.ready = true
				if r.skipped > 0 || offset > 0 {
					log.Infof("webrtc decode start: skipped %d bytes to begin at a keyframe with parameter sets",
						r.skipped+int64(offset))
				}
				r.pending = nil

				return nil
			}
		}

		if len(r.pending) > decodeStartMaxBytes || time.Since(r.start) > decodeStartWait {
			r.gaveUp = true
			r.buf = r.pending
			r.ready = true
			r.pending = nil
			log.Warnf("webrtc decode start: no decodable boundary within %s or %d bytes; passing the stream through as is",
				decodeStartWait, decodeStartMaxBytes)

			return nil
		}

		if err != nil {
			r.buf = r.pending
			r.pending = nil
			r.ready = true

			return nil
		}
	}
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
