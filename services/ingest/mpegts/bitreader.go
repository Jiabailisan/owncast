package mpegts

import "errors"

// errNoMoreData is returned when a bit reader runs out of bytes. Parsers treat
// it as a truncated-but-valid prefix and keep whatever was decoded.
var errNoMoreData = errors.New("mpegts: no more data")

// bitReader is a minimal MSB-first bit reader with Exp-Golomb support for
// H.264 SPS parsing.
type bitReader struct {
	data []byte
	pos  int // bit position
}

func newBitReader(data []byte) *bitReader { return &bitReader{data: data} }

func (r *bitReader) readBit() (uint, error) {
	if r.pos >= len(r.data)*8 {
		return 0, errNoMoreData
	}
	byteIndex := r.pos / 8
	bitIndex := 7 - uint(r.pos%8)
	r.pos++
	return uint((r.data[byteIndex] >> bitIndex) & 1), nil
}

func (r *bitReader) readBits(n int) (uint, error) {
	if n < 0 || n > 32 {
		return 0, errNoMoreData
	}
	var value uint
	for i := 0; i < n; i++ {
		bit, err := r.readBit()
		if err != nil {
			return 0, err
		}
		value = (value << 1) | bit
	}
	return value, nil
}

// readUE reads an unsigned Exp-Golomb code.
func (r *bitReader) readUE() (uint, error) {
	leadingZeros := 0
	for {
		bit, err := r.readBit()
		if err != nil {
			return 0, err
		}
		if bit == 1 {
			break
		}
		leadingZeros++
		if leadingZeros > 31 {
			return 0, errNoMoreData
		}
	}
	if leadingZeros == 0 {
		return 0, nil
	}
	rest, err := r.readBits(leadingZeros)
	if err != nil {
		return 0, err
	}
	return (1 << leadingZeros) - 1 + rest, nil
}

// readSE reads a signed Exp-Golomb code.
func (r *bitReader) readSE() (int, error) {
	code, err := r.readUE()
	if err != nil {
		return 0, err
	}
	if code%2 == 1 {
		return int(code+1) / 2, nil
	}
	return -int(code) / 2, nil
}

func (r *bitReader) skipBits(n int) error {
	for i := 0; i < n; i++ {
		if _, err := r.readBit(); err != nil {
			return err
		}
	}
	return nil
}
