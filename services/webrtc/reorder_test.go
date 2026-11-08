package webrtc

import (
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func queuedPacket(seq uint16) *rtp.Packet {
	return &rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: seq, Timestamp: uint32(seq) * 3000}}
}

func TestReorderBufferEmitsInOrder(t *testing.T) {
	buffer := newReorderBuffer(100 * time.Millisecond)
	now := time.Now()

	ready := buffer.push(queuedPacket(10), now)
	assert.Empty(t, ready, "first packet is held until a neighbour arrives")

	ready = buffer.push(queuedPacket(12), now)
	require.Len(t, ready, 1)
	assert.EqualValues(t, 10, ready[0].SequenceNumber)

	ready = buffer.push(queuedPacket(11), now)
	require.Len(t, ready, 2)
	assert.EqualValues(t, 11, ready[0].SequenceNumber)
	assert.EqualValues(t, 12, ready[1].SequenceNumber)
}

func TestReorderBufferFlushesStalledPacket(t *testing.T) {
	buffer := newReorderBuffer(50 * time.Millisecond)
	now := time.Now()

	ready := buffer.push(queuedPacket(10), now)
	assert.Empty(t, ready)

	ready, forced := buffer.flush(now.Add(60 * time.Millisecond))
	require.Len(t, ready, 1)
	assert.True(t, forced)
	assert.EqualValues(t, 10, ready[0].SequenceNumber)
}
