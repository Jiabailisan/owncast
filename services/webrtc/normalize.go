package webrtc

import "github.com/pion/rtp"

const (
	// maxSequenceGap is the largest in-run sequence leap still treated as
	// packet loss rather than a new producer run.
	maxSequenceGap = 1000

	// timestampGapSeconds bounds an in-run timestamp jump: more than this much
	// media time without a new run means the producer clock restarted.
	timestampGapSeconds = 1
)

// normalizer rebases RTP sequence numbers and timestamps onto a monotonically
// increasing output timeline. FFmpeg restarts (crash, process replacement,
// config change) reset its RTP muxer counters, which would otherwise break the
// browser's jitter buffer and retransmission state for every connected viewer;
// the shared tracks survive restarts, so the forwarder hides them.
//
// It is a pure, single-goroutine value type: one instance per forwarder read
// loop, no locks, fully table-testable.
type normalizer struct {
	clockRate     uint32
	expectedDelta uint32

	initialized bool
	restart     bool

	lastInSeq  uint16
	lastInTS   uint32
	lastOutSeq uint16
	lastOutTS  uint32

	inBaseSeq  uint16
	inBaseTS   uint32
	outBaseSeq uint16
	outBaseTS  uint32
}

func newNormalizer(clockRate, expectedDelta uint32) *normalizer {
	if expectedDelta == 0 {
		expectedDelta = clockRate / 30
	}

	return &normalizer{clockRate: clockRate, expectedDelta: expectedDelta}
}

// markRestart tells the normalizer that the next packet belongs to a new
// producer run even if its counters happen to look contiguous.
func (n *normalizer) markRestart() {
	n.restart = true
}

// apply rewrites the packet counters in place and reports whether the packet
// started a new output run (a rebase).
func (n *normalizer) apply(pkt *rtp.Packet) bool {
	origSeq, origTS := pkt.SequenceNumber, pkt.Timestamp
	rebased := false

	switch {
	case !n.initialized:
		n.startRun(origSeq, origTS)
		n.initialized = true
	case n.restart || n.isDiscontinuity(origSeq, origTS):
		n.startRun(origSeq, origTS)
		rebased = true
	default:
		pkt.SequenceNumber = n.outBaseSeq + (origSeq - n.inBaseSeq)
		pkt.Timestamp = n.outBaseTS + (origTS - n.inBaseTS)
		// Multiple RTP packets of one video frame legitimately share a
		// timestamp; only a regression must be clamped.
		if pkt.Timestamp < n.lastOutTS {
			pkt.Timestamp = n.lastOutTS
		}
	}

	n.restart = false
	if rebased {
		pkt.SequenceNumber = n.outBaseSeq
		pkt.Timestamp = n.outBaseTS
	}

	n.lastInSeq = origSeq
	n.lastInTS = origTS
	n.lastOutSeq = pkt.SequenceNumber
	n.lastOutTS = pkt.Timestamp

	return rebased
}

func (n *normalizer) startRun(inSeq uint16, inTS uint32) {
	n.inBaseSeq = inSeq
	n.inBaseTS = inTS
	if !n.initialized {
		n.outBaseSeq = inSeq
		n.outBaseTS = inTS
	} else {
		n.outBaseSeq = n.lastOutSeq + 1
		n.outBaseTS = n.lastOutTS + n.expectedDelta
	}
}

func (n *normalizer) isDiscontinuity(seq uint16, ts uint32) bool {
	seqDelta := seq - n.lastInSeq
	if seqDelta == 0 {
		// Duplicate or retransmission inside the current run.
		return false
	}
	// A media-clock leap means the producer restarted. The comparison is signed
	// so a packet that is only slightly older (a reorder) does not look like a
	// wrapped-around jump.
	tsDelta := int64(int32(ts - n.lastInTS)) //nolint:gosec // wrap-around is the intended distance arithmetic
	clockLeap := int64(n.clockRate) * timestampGapSeconds
	if tsDelta > clockLeap || tsDelta < -clockLeap {
		return true
	}
	// Otherwise compare signed sequence distances: a small negative distance is a
	// packet that arrived out of order (FFmpeg's loopback UDP socket can reorder
	// under load), not a new run. Rebasing on every reordered packet would jump
	// the timestamp forward and inflate the viewer's jitter buffer.
	signedDelta := int16(seqDelta) //nolint:gosec // wrap-around is the intended distance arithmetic

	return signedDelta > maxSequenceGap || signedDelta < -maxSequenceGap
}
