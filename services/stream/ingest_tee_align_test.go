package stream

import (
	"bytes"
	"testing"
)

// buildFLVStream builds a minimal but structurally valid FLV stream: a file
// header followed by tags with their PreviousTagSize fields.
func buildFLVStream(tagSizes ...int) []byte {
	stream := []byte{'F', 'L', 'V', 0x01, 0x05, 0x00, 0x00, 0x00, 0x09, 0x00, 0x00, 0x00, 0x00}
	for index, size := range tagSizes {
		tagType := byte(9)
		if index%2 == 1 {
			tagType = 8
		}
		header := []byte{tagType, byte(size >> 16), byte(size >> 8), byte(size), 0, 0, 0, 0, 0, 0, 0}
		stream = append(stream, header...)
		payload := bytes.Repeat([]byte{0xAA}, size)
		stream = append(stream, payload...)
		tagSize := 11 + size
		stream = append(stream, byte(tagSize>>24), byte(tagSize>>16), byte(tagSize>>8), byte(tagSize))
	}

	return stream
}

// tagOffsets returns the offset of every tag header in a valid FLV stream.
func tagOffsets(stream []byte) []int {
	offsets := []int{}
	offset := 13
	for offset+11 <= len(stream) {
		offsets = append(offsets, offset)
		offset += 11 + flvTagDataSize(stream[offset:]) + 4
	}

	return offsets
}

// TestFLVAlignerStartsAtATagBoundary proves a branch that begins mid-tag is
// rewritten into a stream FFmpeg can demux: a synthetic file header followed by
// the first whole tag.
func TestFLVAlignerStartsAtATagBoundary(t *testing.T) {
	stream := buildFLVStream(120, 200, 80, 300)
	offsets := tagOffsets(stream)
	if len(offsets) < 3 {
		t.Fatalf("fixture has %d tags, want at least 3", len(offsets))
	}

	// Start in the middle of the first tag's payload, the way a branch added
	// after the stream started sees it.
	cut := offsets[0] + 40
	aligner := newFLVAligner(nil)
	out, ok := aligner.feed(stream[cut:])
	if !ok {
		t.Fatal("the aligner did not find a boundary")
	}

	if !bytes.HasPrefix(out, []byte{'F', 'L', 'V', 0x01}) {
		t.Fatalf("aligned stream does not start with an FLV header: %x", out[:13])
	}
	body := out[len(flvFileHeader):]
	want := stream[offsets[1]:]
	if !bytes.Equal(body, want) {
		t.Fatalf("aligned body starts at the wrong offset: got %d bytes, want the tag at %d (%d bytes)",
			len(body), offsets[1], len(want))
	}

	// The rewritten stream must still be a valid tag chain.
	if _, ok := flvTagBoundary(out); !ok {
		t.Fatal("the aligned stream is not a valid FLV tag chain")
	}
}

// TestFLVAlignerPassesThroughWhenNoBoundaryIsFound keeps a source that is not
// FLV from being buffered forever.
func TestFLVAlignerPassesThroughWhenNoBoundaryIsFound(t *testing.T) {
	aligner := newFLVAligner(nil)
	junk := bytes.Repeat([]byte{0x00}, flvMaxAlignBuffer+1)
	out, ok := aligner.feed(junk)
	if !ok {
		t.Fatal("the aligner kept buffering past its cap")
	}
	if !bytes.Equal(out, junk) {
		t.Fatal("the aligner did not forward the raw bytes")
	}
}

// TestFLVAlignerKeepsBufferingUntilTheBoundaryCanBeConfirmed proves the aligner
// does not emit a candidate it cannot verify with a following tag header.
func TestFLVAlignerKeepsBufferingUntilTheBoundaryCanBeConfirmed(t *testing.T) {
	stream := buildFLVStream(60, 60, 60)
	offsets := tagOffsets(stream)

	aligner := newFLVAligner(nil)
	// Feed everything up to the middle of the second tag: the first tag starts
	// at offsets[0] but its successor cannot be confirmed yet.
	if _, ok := aligner.feed(stream[offsets[0]+5 : offsets[1]+20]); ok {
		t.Fatal("the aligner emitted a boundary it could not confirm")
	}
	if _, ok := aligner.feed(stream[offsets[1]+20:]); !ok {
		t.Fatal("the aligner never confirmed the boundary")
	}
}

// TestFLVAlignerUsesTheCachedContainerPrefix proves a late-joining branch is
// handed the decoder configuration the session cached. FLV sends it once, so
// without it FFmpeg has no extradata and cannot decode the stream.
func TestFLVAlignerUsesTheCachedContainerPrefix(t *testing.T) {
	stream := buildFLVStream(120, 200, 80)
	offsets := tagOffsets(stream)
	prefix := append([]byte{'F', 'L', 'V', 0x01, 0x05, 0x00, 0x00, 0x00, 0x09, 0x00, 0x00, 0x00, 0x00}, []byte{9, 0, 0, 5, 0, 0, 0, 0, 0, 0, 0, 0x17, 0, 0, 0, 0, 1, 2, 3}...)

	aligner := newFLVAligner(prefix)
	out, ok := aligner.feed(stream[offsets[0]+40:])
	if !ok {
		t.Fatal("the aligner did not find a boundary")
	}
	if !bytes.HasPrefix(out, prefix) {
		t.Fatal("the branch was not given the cached container prefix")
	}
	if !bytes.Equal(out[len(prefix):], stream[offsets[1]:]) {
		t.Fatal("the aligned body does not start at the next tag")
	}
}
