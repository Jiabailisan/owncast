package mpegts

import "testing"

func TestPESAccumulatorEmptyPayloadPUSIFlushesPreviousAndArmsPTS(t *testing.T) {
	var acc pesAccumulator

	// First PES at PTS=90000 with 1000 bytes.
	if _, _, flush := acc.addPacket(1000, 90000, true, true); flush {
		t.Fatal("first PES must not flush")
	}

	// Next PES starts with PUSI=1 and 0 ES bytes in the header packet (PTS=180000).
	flushPTS, flushBytes, flush := acc.addPacket(0, 180000, true, true)
	if !flush || flushPTS != 90000 || flushBytes != 1000 {
		t.Fatalf("empty PUSI=1 flush = (%d, %d, %v), want (90000, 1000, true)", flushPTS, flushBytes, flush)
	}

	// Continuation packet adds 2000 bytes to the new PES armed with PTS=180000.
	if _, _, flush := acc.addPacket(2000, 0, false, false); flush {
		t.Fatal("continuation packet must not flush")
	}

	// Third PES flushes the 2000-byte PES at PTS=180000.
	flushPTS, flushBytes, flush = acc.addPacket(500, 270000, true, true)
	if !flush || flushPTS != 180000 || flushBytes != 2000 {
		t.Fatalf("second flush = (%d, %d, %v), want (180000, 2000, true)", flushPTS, flushBytes, flush)
	}
}
