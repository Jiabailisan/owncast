package webrtc

import (
	"testing"

	"github.com/pion/rtp"
	"github.com/stretchr/testify/assert"
)

func videoPacket(seq uint16, ts uint32) *rtp.Packet {
	return &rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: seq, Timestamp: ts, SSRC: 0x11223344}}
}

func TestNormalizerFirstPacketAndContiguousRun(t *testing.T) {
	n := newNormalizer(90000, 3000)

	pkt := videoPacket(5000, 90000)
	assert.False(t, n.apply(pkt))
	assert.EqualValues(t, 5000, pkt.SequenceNumber)
	assert.EqualValues(t, 90000, pkt.Timestamp)

	pkt = videoPacket(5001, 93000)
	assert.False(t, n.apply(pkt))
	assert.EqualValues(t, 5001, pkt.SequenceNumber)
	assert.EqualValues(t, 93000, pkt.Timestamp)

	// Two packets of the same video frame share a timestamp: no clamping.
	pkt = videoPacket(5002, 93000)
	assert.False(t, n.apply(pkt))
	assert.EqualValues(t, 93000, pkt.Timestamp)
}

func TestNormalizerPacketLossWithinRunKeepsGap(t *testing.T) {
	n := newNormalizer(90000, 3000)
	pkt := videoPacket(100, 0)
	n.apply(pkt)

	pkt = videoPacket(103, 9000) // two packets lost
	assert.False(t, n.apply(pkt))
	assert.EqualValues(t, 103, pkt.SequenceNumber)
	assert.EqualValues(t, 9000, pkt.Timestamp)
}

func TestNormalizerRebaseOnExplicitRestart(t *testing.T) {
	n := newNormalizer(90000, 3000)
	n.apply(videoPacket(100, 1000))
	n.apply(videoPacket(101, 4000))

	n.markRestart()
	pkt := videoPacket(65500, 0)
	assert.True(t, n.apply(pkt))
	assert.EqualValues(t, 102, pkt.SequenceNumber)
	assert.EqualValues(t, 7000, pkt.Timestamp)

	// The next packet of the new run keeps the offset.
	pkt = videoPacket(65501, 3000)
	assert.False(t, n.apply(pkt))
	assert.EqualValues(t, 103, pkt.SequenceNumber)
	assert.EqualValues(t, 10000, pkt.Timestamp)
}

func TestNormalizerRebaseOnSequenceDiscontinuity(t *testing.T) {
	n := newNormalizer(90000, 3000)
	n.apply(videoPacket(10, 100))
	n.apply(videoPacket(11, 3100))

	// A leap larger than maxSequenceGap (new FFmpeg run with a random start).
	pkt := videoPacket(40000, 200)
	assert.True(t, n.apply(pkt))
	assert.EqualValues(t, 12, pkt.SequenceNumber)
	assert.EqualValues(t, 6100, pkt.Timestamp)
}

func TestNormalizerRebaseOnTimestampJump(t *testing.T) {
	n := newNormalizer(90000, 3000)
	n.apply(videoPacket(10, 90))
	n.apply(videoPacket(11, 3090))

	// More than one second of media time without a new run.
	pkt := videoPacket(12, 90000*5)
	assert.True(t, n.apply(pkt))
	assert.EqualValues(t, 12, pkt.SequenceNumber)
	assert.EqualValues(t, 6090, pkt.Timestamp)
}

func TestNormalizerSequenceWrap(t *testing.T) {
	n := newNormalizer(90000, 3000)
	n.apply(videoPacket(65534, 1000))
	n.apply(videoPacket(65535, 4000))

	pkt := videoPacket(0, 7000) // wraps around
	assert.False(t, n.apply(pkt))
	assert.EqualValues(t, 0, pkt.SequenceNumber)
	assert.EqualValues(t, 7000, pkt.Timestamp)
}

func TestNormalizerAudioDeltas(t *testing.T) {
	n := newNormalizer(48000, 960)
	n.apply(videoPacket(100, 48000))
	n.markRestart()
	pkt := videoPacket(0, 0)
	assert.True(t, n.apply(pkt))
	assert.EqualValues(t, 101, pkt.SequenceNumber)
	assert.EqualValues(t, 48960, pkt.Timestamp)
}
