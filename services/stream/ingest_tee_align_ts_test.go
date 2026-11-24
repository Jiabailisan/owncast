package stream

import (
	"bytes"
	"testing"
)

// tsPacket builds a 188-byte TS packet with the given PID and a payload of
// filler bytes.
func tsPacket(pid uint16, filler byte) []byte {
	pkt := make([]byte, 188)
	pkt[0] = 0x47
	pkt[1] = byte(pid>>8) & 0x1f
	pkt[2] = byte(pid)
	pkt[3] = 0x10 // payload only
	for index := 4; index < 188; index++ {
		pkt[index] = filler
	}

	return pkt
}

// TestTSSyncOffsetFindsTheBoundary proves the TS aligner locates the first
// position that starts a run of 188-periodic sync bytes.
func TestTSSyncOffsetFindsTheBoundary(t *testing.T) {
	good := bytes.Join([][]byte{tsPacket(0, 1), tsPacket(0x100, 2), tsPacket(0x100, 3), tsPacket(0x100, 4)}, nil)
	junk := []byte{0xb3, 0x3f, 0xd5, 0x65, 0x39, 0x82, 0x2e, 0xfc} // 8 garbage bytes

	stream := append(append([]byte{}, junk...), good...)
	offset := tsSyncOffset(stream)
	if offset != len(junk) {
		t.Fatalf("sync offset = %d, want %d", offset, len(junk))
	}

	offset = tsSyncOffset(good)
	if offset != 0 {
		t.Fatalf("aligned stream: sync offset = %d, want 0", offset)
	}

	if got := tsSyncOffset(junk); got != -1 {
		t.Fatalf("junk only: sync offset = %d, want -1", got)
	}
}

// TestTSAlignerDropsThePartialHead is the regression test for the isolated
// WebRTC process: a branch that joins mid-packet received 40 garbage bytes
// before the first real TS packet, which desynchronized the demuxer and cost
// the stream its probe ("non-existing PPS 0", "unspecified size"). The aligner
// must drop everything before the first whole packet.
func TestTSAlignerDropsThePartialHead(t *testing.T) {
	good := bytes.Join([][]byte{
		tsPacket(0, 1),     // PAT
		tsPacket(0x100, 2), // video
		tsPacket(0x100, 3),
		tsPacket(0x100, 4),
	}, nil)
	// A branch that joined in the middle of a packet: 40 bytes of the previous
	// packet's tail, then the aligned stream.
	aligned, ok := newTSAligner().feed(append(bytes.Repeat([]byte{0xAA}, 40), good...))
	if !ok {
		t.Fatal("the aligner did not find the boundary")
	}
	if !bytes.Equal(aligned, good) {
		t.Fatalf("aligned output is %d bytes, want the clean %d-byte stream with the 40-byte head dropped",
			len(aligned), len(good))
	}
}

// TestTSAlignerBuffersUntilTheBoundaryIsComplete proves a boundary that spans
// two feeds is still found: the aligner must not emit a half-verified head.
func TestTSAlignerBuffersUntilTheBoundaryIsComplete(t *testing.T) {
	aligner := newTSAligner()
	good := bytes.Join([][]byte{tsPacket(0, 1), tsPacket(0x100, 2), tsPacket(0x100, 3), tsPacket(0x100, 4), tsPacket(0x100, 5)}, nil)

	// Feed the garbage plus the first packet only: the run of sync bytes cannot
	// be verified yet.
	if _, ok := aligner.feed(append(bytes.Repeat([]byte{0xAA}, 40), good[:188]...)); ok {
		t.Fatal("the aligner emitted before the sync run was verified")
	}
	aligned, ok := aligner.feed(good[188:])
	if !ok {
		t.Fatal("the aligner never confirmed the boundary")
	}
	if !bytes.Equal(aligned, good) {
		t.Fatalf("aligned output is not the clean stream: %d bytes", len(aligned))
	}
}
