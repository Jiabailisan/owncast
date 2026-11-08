package webrtc

import (
	"time"

	"github.com/pion/rtp"
)

type pendingPacket struct {
	packet *rtp.Packet
	at     time.Time
}

// reorderBuffer holds a small window of RTP packets so a burst that arrives out
// of order from FFmpeg's loopback socket is emitted in sequence order. It is a
// single-goroutine value type owned by one forwarder.
type reorderBuffer struct {
	hold        time.Duration
	nextSeq     uint16
	initialized bool
	pending     []pendingPacket
}

func newReorderBuffer(hold time.Duration) *reorderBuffer {
	return &reorderBuffer{hold: hold}
}

// push buffers a packet and returns the packets that are ready to emit in
// sequence order.
func (b *reorderBuffer) push(packet *rtp.Packet, now time.Time) []*rtp.Packet {
	clone := *packet
	clone.Payload = append([]byte(nil), packet.Payload...)
	b.pending = append(b.pending, pendingPacket{packet: &clone, at: now})
	if !b.initialized {
		// Hold the first packet until a neighbour arrives or the window
		// expires; this is what lets a first-arriving later packet be put
		// back in order.
		b.nextSeq = clone.SequenceNumber
		b.initialized = true

		return nil
	}

	return b.drain()
}

// flush releases packets that have waited longer than the reorder window, in
// arrival order, then emits any newly contiguous packets.
func (b *reorderBuffer) flush(now time.Time) (ready []*rtp.Packet, forced bool) {
	for len(b.pending) > 0 {
		head := b.pending[0]
		if now.Sub(head.at) < b.hold {
			break
		}
		ready = append(ready, head.packet)
		b.pending = b.pending[1:]
		b.nextSeq = head.packet.SequenceNumber + 1
		forced = true
		ready = append(ready, b.drain()...)
	}

	return ready, forced
}

func (b *reorderBuffer) drain() []*rtp.Packet {
	var ready []*rtp.Packet
	for {
		index := -1
		for i := range b.pending {
			if b.pending[i].packet.SequenceNumber == b.nextSeq {
				index = i
				break
			}
		}
		if index < 0 {
			return ready
		}
		ready = append(ready, b.pending[index].packet)
		b.pending = append(b.pending[:index], b.pending[index+1:]...)
		b.nextSeq++
	}
}
