package stream

import (
	"bytes"
	"io"
	"testing"
)

func buildValidFLVTag(tagType byte, body []byte) []byte {
	size := 11 + len(body)
	out := make([]byte, 0, size+4)
	out = append(out, tagType, byte(len(body)>>16), byte(len(body)>>8), byte(len(body)))
	out = append(out, 0, 0, 0, 0) // timestamp
	out = append(out, 0, 0, 0)    // streamID
	out = append(out, body...)
	out = append(out, byte(size>>24), byte(size>>16), byte(size>>8), byte(size))
	return out
}

func TestFLVTagBoundaryValidatesPreviousTagSize(t *testing.T) {
	// Fake candidate tag header with wrong PreviousTagSize, followed by two genuine FLV tags.
	fake := buildValidFLVTag(9, []byte{0x17, 0x01, 0x02})
	fake[len(fake)-1] ^= 0xFF // corrupt PreviousTagSize

	tag1 := buildValidFLVTag(9, []byte{0x17, 0x01, 0x00, 0x00, 0x00, 0xAA})
	tag2 := buildValidFLVTag(8, []byte{0xAF, 0x01, 0xBB, 0xCC})

	stream := append(append(append([]byte{}, fake...), tag1...), tag2...)
	offset, ok := flvTagBoundary(stream)
	if !ok {
		t.Fatal("flvTagBoundary failed to locate valid FLV boundary")
	}
	if offset != len(fake) {
		t.Fatalf("offset = %d, want %d (must reject fake tag with mismatched PreviousTagSize)", offset, len(fake))
	}
}

func TestIngestTeePendingLedgerStaysZeroAfterFLVAlignerAndEndChAligns(t *testing.T) {
	tag1 := buildValidFLVTag(9, []byte{0x17, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02})
	tag2 := buildValidFLVTag(8, []byte{0xAF, 0x01, 0x03, 0x04})
	junk := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	payload := append(append(append([]byte{}, junk...), tag1...), tag2...)

	prefix := []byte{'F', 'L', 'V', 0x01, 0x01, 0x00, 0x00, 0x00, 0x09, 0x00, 0x00, 0x00, 0x00}
	tee := newIngestTee(bytes.NewReader(payload))
	reader := tee.AddLateBranch("webrtc", false, nil, "flv", prefix)
	branch := tee.Branch("webrtc")
	tee.Start()

	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	want := append(append([]byte{}, prefix...), append(tag1, tag2...)...)
	if !bytes.Equal(got, want) {
		t.Fatalf("aligned output mismatch: got %d bytes, want %d bytes", len(got), len(want))
	}
	if pending := branch.pending.Load(); pending != 0 {
		t.Fatalf("branch.pending = %d after full drain, want 0 (aligner rewrite must not drift pending ledger)", pending)
	}
}
