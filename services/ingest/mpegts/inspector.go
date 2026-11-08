// Package mpegts is a minimal MPEG-TS / H.264 inspector for SRT ingest.
//
// It consumes the MPEG-TS byte stream produced by an SRT publisher and extracts
// the source properties Owncast needs: video/audio codec, H.264 profile and
// level, resolution, frame rate and whether the stream uses B-frames (used for
// WebRTC copy eligibility). It is deliberately a probe, not a decoder: it never
// buffers more than a bounded amount of data and ignores anything it cannot
// parse.
package mpegts

import (
	"encoding/binary"
	"sync"
)

const (
	// tsPacketSize is the fixed MPEG-TS packet size.
	tsPacketSize = 188
	// tsSyncByte starts every MPEG-TS packet.
	tsSyncByte = 0x47
	// pmtPIDMask extracts the 13-bit PID from two header bytes.
	pmtPIDMask = 0x1fff
	// maxESBuffer caps the retained elementary-stream tail.
	maxESBuffer = 8 << 20
	// slicesForBFrameDecision is how many slice headers to inspect before
	// reporting the B-frame verdict.
	slicesForBFrameDecision = 60
)

// MPEG-TS stream type values we classify.
const (
	streamTypeH264        = 0x1b
	streamTypeHEVC        = 0x24
	streamTypeMPEG1Video  = 0x01
	streamTypeMPEG2Video  = 0x02
	streamTypeMPEG4Video  = 0x10
	streamTypeAACADTS     = 0x0f
	streamTypeAACLATM     = 0x11
	streamTypeMP3a        = 0x03
	streamTypeMP3b        = 0x04
	streamTypeAC3         = 0x81
	streamTypeEAC3        = 0x87
)

// SourceInfo is the inspected source properties. Zero values mean "unknown".
type SourceInfo struct {
	VideoCodec    string
	AudioCodec    string
	Profile       string
	Level         string
	Width         int
	Height        int
	FPS           float64
	FPSValid      bool
	HasBFrames    bool
	BFrameChecked bool
	VideoStreamTY byte
	AudioStreamTY byte
}

// Inspector is an io.Writer that inspects an MPEG-TS stream. It is safe for
// concurrent Write/Snapshot use.
type Inspector struct {
	mu sync.RWMutex

	carry []byte

	pmtPID   uint16
	pmtFound bool
	videoPID uint16
	audioPID uint16

	es         []byte
	sliceTypes map[uint]int
	sliceCount int

	info SourceInfo
}

// NewInspector returns an empty inspector.
func NewInspector() *Inspector {
	return &Inspector{
		sliceTypes: make(map[uint]int),
	}
}

// Write consumes MPEG-TS bytes. It always reports success; malformed data is
// ignored so a corrupt packet cannot terminate ingest.
func (i *Inspector) Write(p []byte) (int, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	buf := append(i.carry, p...)
	consumed := 0
	for len(buf)-consumed >= tsPacketSize {
		// Resynchronise on the next packet boundary if needed.
		if buf[consumed] != tsSyncByte {
			next := -1
			for offset := 1; offset < len(buf)-consumed; offset++ {
				if buf[consumed+offset] == tsSyncByte {
					next = consumed + offset
					break
				}
			}
			if next < 0 {
				consumed = len(buf)
				break
			}
			consumed = next
			continue
		}
		packet := buf[consumed : consumed+tsPacketSize]
		i.consumePacket(packet)
		consumed += tsPacketSize
	}

	i.carry = append(i.carry[:0], buf[consumed:]...)
	return len(p), nil
}

func (i *Inspector) consumePacket(packet []byte) {
	header1 := packet[1]
	payloadStart := header1&0x40 != 0
	pid := binary.BigEndian.Uint16(packet[1:3]) & pmtPIDMask
	adaptation := (header1 >> 4) & 0x03

	offset := 4
	if adaptation == 2 {
		return // adaptation field only, no payload
	}
	if adaptation == 3 {
		offset += 1 + int(packet[4])
	}
	if offset >= tsPacketSize {
		return
	}
	payload := packet[offset:]

	switch {
	case pid == 0:
		i.parsePAT(payload, payloadStart)
	case i.pmtFound && pid == i.pmtPID:
		i.parsePMT(payload, payloadStart)
	case i.videoPID != 0 && pid == i.videoPID:
		i.es = append(i.es, payload...)
		if len(i.es) > maxESBuffer {
			i.es = append([]byte(nil), i.es[len(i.es)-maxESBuffer/2:]...)
		}
		i.scanNALs()
	case i.audioPID != 0 && pid == i.audioPID:
		i.classifyAudio()
	}
}

func (i *Inspector) tablePayload(payload []byte, payloadStart bool) []byte {
	if !payloadStart || len(payload) < 1 {
		return nil
	}
	pointer := int(payload[0])
	if pointer+1 >= len(payload) {
		return nil
	}
	return payload[1+pointer:]
}

func (i *Inspector) parsePAT(payload []byte, payloadStart bool) {
	table := i.tablePayload(payload, payloadStart)
	if table == nil || len(table) < 12 || table[0] != 0x00 {
		return
	}
	sectionLength := int(binary.BigEndian.Uint16(table[1:3]) & 0x0fff)
	end := 3 + sectionLength - 4 // minus CRC
	if end > len(table) {
		end = len(table)
	}
	for pos := 8; pos+4 <= end; pos += 4 {
		programNumber := binary.BigEndian.Uint16(table[pos : pos+2])
		programPID := binary.BigEndian.Uint16(table[pos+2:pos+4]) & pmtPIDMask
		if programNumber != 0 {
			i.pmtPID = programPID
			i.pmtFound = true
			return
		}
	}
}

func (i *Inspector) parsePMT(payload []byte, payloadStart bool) {
	table := i.tablePayload(payload, payloadStart)
	if table == nil || len(table) < 12 || table[0] != 0x02 {
		return
	}
	sectionLength := int(binary.BigEndian.Uint16(table[1:3]) & 0x0fff)
	end := 3 + sectionLength - 4
	if end > len(table) {
		end = len(table)
	}
	programInfoLength := int(binary.BigEndian.Uint16(table[10:12]) & 0x0fff)
	pos := 12 + programInfoLength
	for pos+5 <= end {
		streamType := table[pos]
		elementaryPID := binary.BigEndian.Uint16(table[pos+1:pos+3]) & pmtPIDMask
		esInfoLength := int(binary.BigEndian.Uint16(table[pos+3:pos+5]) & 0x0fff)
		pos += 5 + esInfoLength

		switch streamType {
		case streamTypeH264, streamTypeHEVC, streamTypeMPEG1Video, streamTypeMPEG2Video, streamTypeMPEG4Video:
			if i.videoPID == 0 {
				i.videoPID = elementaryPID
				i.info.VideoStreamTY = streamType
				if streamType == streamTypeH264 {
					i.info.VideoCodec = "h264"
				} else {
					i.info.VideoCodec = videoCodecName(streamType)
				}
			}
		case streamTypeAACADTS, streamTypeAACLATM, streamTypeMP3a, streamTypeMP3b, streamTypeAC3, streamTypeEAC3:
			if i.audioPID == 0 {
				i.audioPID = elementaryPID
				i.info.AudioStreamTY = streamType
				i.info.AudioCodec = audioCodecName(streamType)
			}
		}
	}
}

func (i *Inspector) classifyAudio() {
	if i.info.AudioCodec == "" {
		i.info.AudioCodec = audioCodecName(i.info.AudioStreamTY)
	}
}

func (i *Inspector) scanNALs() {
	buf := i.es
	searchFrom := 0
	consumedUpTo := 0

	for {
		start, startLen := findStartCode(buf, searchFrom)
		if start < 0 {
			break
		}
		next, _ := findStartCode(buf, start+startLen+1)
		if next < 0 {
			break
		}
		nal := buf[start+startLen : next]
		i.handleNAL(nal)
		consumedUpTo = next
		searchFrom = next
	}

	if consumedUpTo > 0 {
		i.es = append([]byte(nil), buf[consumedUpTo:]...)
	}
}

// findStartCode returns the index and length (3 or 4) of the next Annex-B start
// code at or after from, or -1.
func findStartCode(buf []byte, from int) (int, int) {
	if from < 0 {
		from = 0
	}
	for i := from; i+3 <= len(buf); i++ {
		if buf[i] != 0x00 || buf[i+1] != 0x00 {
			continue
		}
		if buf[i+2] == 0x01 {
			return i, 3
		}
		if i+4 <= len(buf) && buf[i+2] == 0x00 && buf[i+3] == 0x01 {
			return i, 4
		}
	}
	return -1, 0
}

func (i *Inspector) handleNAL(nal []byte) {
	if len(nal) < 1 {
		return
	}
	nalType := nal[0] & 0x1f
	switch nalType {
	case 7: // SPS
		if sps, ok := ParseSPS(unescapeEmulationPrevention(nal[1:])); ok {
			i.info.VideoCodec = "h264"
			i.info.Profile = sps.ProfileName()
			i.info.Level = sps.Level()
			i.info.Width = sps.Width
			i.info.Height = sps.Height
			if sps.FPSValid {
				i.info.FPS = sps.FPS
				i.info.FPSValid = true
			}
		}
	case 1, 5: // coded slice (non-IDR / IDR)
		r := newBitReader(unescapeEmulationPrevention(nal[1:]))
		if _, err := r.readUE(); err != nil { // first_mb_in_slice
			return
		}
		sliceType, err := r.readUE() // slice_type
		if err != nil {
			return
		}
		sliceType %= 5
		i.sliceTypes[sliceType]++
		i.sliceCount++
		if sliceType == 1 {
			i.info.HasBFrames = true
			i.info.BFrameChecked = true
		} else if i.sliceCount >= slicesForBFrameDecision && !i.info.HasBFrames {
			// Enough non-B slices observed to call it a no-B-frame stream.
			i.info.BFrameChecked = true
		}
	}
}

// Snapshot returns a copy of the current source properties.
func (i *Inspector) Snapshot() SourceInfo {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.info
}

// SliceTypeHistogram returns the observed H.264 slice types for diagnostics.
// Keys are normalized slice types: 0=P, 1=B, 2=I, 3=SP, 4=SI.
func (i *Inspector) SliceTypeHistogram() map[uint]int {
	i.mu.RLock()
	defer i.mu.RUnlock()
	out := make(map[uint]int, len(i.sliceTypes))
	for k, v := range i.sliceTypes {
		out[k] = v
	}
	return out
}

func videoCodecName(streamType byte) string {
	switch streamType {
	case streamTypeHEVC:
		return "hevc"
	case streamTypeMPEG1Video:
		return "mpeg1video"
	case streamTypeMPEG2Video:
		return "mpeg2video"
	case streamTypeMPEG4Video:
		return "mpeg4"
	default:
		return "unknown"
	}
}

func audioCodecName(streamType byte) string {
	switch streamType {
	case streamTypeAACADTS, streamTypeAACLATM:
		return "aac"
	case streamTypeMP3a, streamTypeMP3b:
		return "mp3"
	case streamTypeAC3:
		return "ac3"
	case streamTypeEAC3:
		return "eac3"
	default:
		return "unknown"
	}
}
// unescapeEmulationPrevention removes H.264 emulation-prevention bytes
// (0x03 after two 0x00 bytes) so bit-level parsing sees the original RBSP.
func unescapeEmulationPrevention(data []byte) []byte {
	out := make([]byte, 0, len(data))
	zeros := 0
	for _, b := range data {
		if zeros >= 2 && b == 0x03 {
			zeros = 0
			continue
		}
		out = append(out, b)
		if b == 0 {
			zeros++
		} else {
			zeros = 0
		}
	}
	return out
}
