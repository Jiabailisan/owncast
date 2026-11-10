package rtmp

import (
	"encoding/binary"

	"github.com/nareix/joy5/av"
	"github.com/owncast/owncast/models"
)

// h264SourceInspector derives the H.264 source properties that RTMP metadata
// cannot provide (profile, level, dimensions and B-frame usage) from the FLV
// video packets. The stream bytes are passed through to ffmpeg unchanged; the
// inspector only reads the AVC decoder configuration and slice headers.
type h264SourceInspector struct {
	nalLengthSize int
	slicesSeen    int
	hasBFrames    bool
	checked       bool
	profile       string
	level         string
	width         int
	height        int
	changed       bool
}

const slicesForRTMPBFrameDecision = 30

func newH264SourceInspector() *h264SourceInspector {
	return &h264SourceInspector{nalLengthSize: 4}
}

// Observe inspects one demuxed FLV packet. It returns true when the source
// properties changed and should be published to the ingest session.
func (i *h264SourceInspector) Observe(pkt av.Packet) bool {
	i.changed = false
	switch pkt.Type {
	case av.H264DecoderConfig:
		i.observeDecoderConfig(pkt.Data)
	case av.H264:
		i.observeNALUs(pkt.Data)
	}
	return i.changed
}

func (i *h264SourceInspector) observeDecoderConfig(data []byte) {
	if len(data) < 7 || data[0] != 1 {
		return
	}
	i.nalLengthSize = int(data[4]&0x03) + 1
	if profile := h264ProfileName(data[1]); profile != "" && profile != i.profile {
		i.profile = profile
		i.changed = true
	}
	if level := h264LevelName(data[3]); level != "" && level != i.level {
		i.level = level
		i.changed = true
	}
	// The decoder configuration record stores each SPS with a 2-byte length.
	offset := 6
	for spsCount := int(data[5] & 0x1f); spsCount > 0 && offset+2 <= len(data); spsCount-- {
		spsLen := int(binary.BigEndian.Uint16(data[offset:]))
		offset += 2
		if spsLen <= 0 || offset+spsLen > len(data) {
			return
		}
		if width, height, ok := parseSPSDimensions(data[offset : offset+spsLen]); ok {
			if width != i.width || height != i.height {
				i.width, i.height = width, height
				i.changed = true
			}
		}
		offset += spsLen
	}
}

func (i *h264SourceInspector) observeNALUs(data []byte) {
	offset := 0
	for offset+i.nalLengthSize <= len(data) {
		size := 0
		for n := 0; n < i.nalLengthSize; n++ {
			size = size<<8 | int(data[offset+n])
		}
		offset += i.nalLengthSize
		if size <= 0 || offset+size > len(data) {
			return
		}
		nal := data[offset : offset+size]
		offset += size
		if len(nal) == 0 {
			continue
		}
		nalType := nal[0] & 0x1f
		if nalType == 1 || nalType == 5 {
			if !i.checked {
				i.slicesSeen++
				if sliceTypeIsB(nal) {
					i.hasBFrames = true
					i.checked = true
					i.changed = true
				} else if i.slicesSeen >= slicesForRTMPBFrameDecision {
					i.checked = true
					i.changed = true
				}
			}
		}
	}
}

// SourceDetails returns the inspected properties in the shared model shape.
// Nil/zero fields mean "not known yet" and keep copy eligibility conservative.
func (i *h264SourceInspector) SourceDetails() (videoProfile, videoLevel string, width, height int, hasBFrames *bool) {
	videoProfile, videoLevel, width, height = i.profile, i.level, i.width, i.height
	if i.checked {
		value := i.hasBFrames
		hasBFrames = &value
	}
	return
}

func h264ProfileName(profileIDC byte) string {
	switch profileIDC {
	case 66:
		return "baseline"
	case 77:
		return "main"
	case 100:
		return "high"
	case 244:
		return "high"
	default:
		return ""
	}
}

func h264LevelName(levelIDC byte) string {
	if levelIDC == 0 {
		return ""
	}
	return string(rune('0'+levelIDC/10)) + "." + string(rune('0'+levelIDC%10))
}

// sliceTypeIsB parses the first two exp-Golomb fields of an H.264 slice
// header (first_mb_in_slice, slice_type) and reports B/P/etc.
func sliceTypeIsB(nal []byte) bool {
	payload := stripEmulationPrevention(nal[1:])
	if len(payload) == 0 {
		return false
	}
	reader := bitReader{data: payload}
	if _, ok := reader.ue(); !ok {
		return false
	}
	sliceType, ok := reader.ue()
	if !ok {
		return false
	}
	return sliceType == 1 || sliceType == 6
}

func stripEmulationPrevention(data []byte) []byte {
	out := make([]byte, 0, len(data))
	for index := 0; index < len(data); index++ {
		if index >= 2 && data[index] == 3 && data[index-1] == 0 && data[index-2] == 0 {
			continue
		}
		out = append(out, data[index])
	}
	return out
}

type bitReader struct {
	data []byte
	pos  int
}

func (b *bitReader) bit() (uint, bool) {
	if b.pos/8 >= len(b.data) {
		return 0, false
	}
	value := (b.data[b.pos/8] >> (7 - uint(b.pos%8))) & 1
	b.pos++
	return uint(value), true
}

func (b *bitReader) ue() (uint, bool) {
	zeros := 0
	for {
		bitValue, ok := b.bit()
		if !ok {
			return 0, false
		}
		if bitValue == 1 {
			break
		}
		zeros++
		if zeros > 31 {
			return 0, false
		}
	}
	value := uint(1)
	for index := 0; index < zeros; index++ {
		bitValue, ok := b.bit()
		if !ok {
			return 0, false
		}
		value = (value << 1) | bitValue
	}
	return value - 1, true
}

// parseSPSDimensions parses pic_width_in_mbs_minus1 and
// pic_height_in_map_units_minus1 from an H.264 SPS (including the NAL header).
//
//nolint:cyclop // SPS bitstream parsing is an inherently branchy spec walk
func parseSPSDimensions(sps []byte) (int, int, bool) {
	if len(sps) < 4 || sps[0]&0x1f != 7 {
		return 0, 0, false
	}
	payload := stripEmulationPrevention(sps[1:])
	reader := bitReader{data: payload}
	profileIDC, ok := reader.bits(8)
	if !ok {
		return 0, 0, false
	}
	if _, ok = reader.bits(8); !ok { // constraint flags + reserved
		return 0, 0, false
	}
	if _, ok = reader.bits(8); !ok { // level_idc
		return 0, 0, false
	}
	if _, ok = reader.ue(); !ok { // seq_parameter_set_id
		return 0, 0, false
	}
	if profileIDC == 100 || profileIDC == 110 || profileIDC == 122 || profileIDC == 244 || profileIDC == 44 || profileIDC == 83 || profileIDC == 86 || profileIDC == 118 || profileIDC == 128 || profileIDC == 138 || profileIDC == 139 || profileIDC == 134 || profileIDC == 135 {
		chromaFormatIDC, ok := reader.ue()
		if !ok {
			return 0, 0, false
		}
		if chromaFormatIDC == 3 {
			if _, ok = reader.bits(1); !ok {
				return 0, 0, false
			}
		}
		if _, ok = reader.ue(); !ok { // bit_depth_luma_minus8
			return 0, 0, false
		}
		if _, ok = reader.ue(); !ok { // bit_depth_chroma_minus8
			return 0, 0, false
		}
		if _, ok = reader.bits(1); !ok { // qpprime_y_zero_transform_bypass_flag
			return 0, 0, false
		}
		if seqScalingMatrixPresent, ok := reader.bits(1); !ok {
			return 0, 0, false
		} else if seqScalingMatrixPresent == 1 {
			scalingListCount := 8
			if chromaFormatIDC != 3 {
				scalingListCount = 8
			}
			for index := 0; index < scalingListCount; index++ {
				present, ok := reader.bits(1)
				if !ok {
					return 0, 0, false
				}
				if present == 1 {
					size := 16
					if index >= 6 {
						size = 64
					}
					lastScale := int64(8)
					nextScale := int64(8)
					for j := 0; j < size; j++ {
						if nextScale != 0 {
							delta, ok := reader.se()
							if !ok {
								return 0, 0, false
							}
							nextScale = (lastScale + delta + 256) % 256
						}
						if nextScale != 0 {
							lastScale = nextScale
						}
					}
				}
			}
		}
	}
	if _, ok = reader.ue(); !ok { // log2_max_frame_num_minus4
		return 0, 0, false
	}
	pocType, ok := reader.ue()
	if !ok {
		return 0, 0, false
	}
	switch pocType {
	case 0:
		if _, ok = reader.ue(); !ok {
			return 0, 0, false
		}
	case 1:
		if _, ok = reader.bits(1); !ok {
			return 0, 0, false
		}
		if _, ok = reader.se(); !ok {
			return 0, 0, false
		}
		if _, ok = reader.se(); !ok {
			return 0, 0, false
		}
		cycle, ok := reader.ue()
		if !ok || cycle > 255 {
			return 0, 0, false
		}
		for index := uint(0); index < cycle; index++ {
			if _, ok = reader.se(); !ok {
				return 0, 0, false
			}
		}
	}
	if _, ok = reader.ue(); !ok { // max_num_ref_frames
		return 0, 0, false
	}
	if _, ok = reader.bits(1); !ok { // gaps_in_frame_num_value_allowed_flag
		return 0, 0, false
	}
	widthMbs, ok := reader.ue()
	if !ok {
		return 0, 0, false
	}
	heightMapUnits, ok := reader.ue()
	if !ok {
		return 0, 0, false
	}
	frameMbsOnly, ok := reader.bits(1)
	if !ok {
		return 0, 0, false
	}
	height := int(heightMapUnits+1) * 16 * (2 - int(frameMbsOnly))
	return int(widthMbs+1) * 16, height, true
}

func (b *bitReader) bits(count int) (uint, bool) {
	var value uint
	for index := 0; index < count; index++ {
		bitValue, ok := b.bit()
		if !ok {
			return 0, false
		}
		value = (value << 1) | bitValue
	}
	return value, true
}

func (b *bitReader) se() (int64, bool) {
	value, ok := b.ue()
	if !ok {
		return 0, false
	}
	// The bit reader bounds the prefix to 31 bits, so the value fits int64.
	magnitude := int64((value + 1) / 2) //nolint:gosec // bounded by the ue prefix length
	if value%2 == 0 {
		return -magnitude, true
	}
	return magnitude, true
}

// ApplySourceInspection publishes the inspector result on a live RTMP session.
// The existing onMetaData details are preserved; only the fields RTMP metadata
// cannot provide are filled in.
func (s *session) applySourceInspection(inspector *h264SourceInspector) {
	profile, level, width, height, hasBFrames := inspector.SourceDetails()
	if profile == "" && level == "" && width == 0 && hasBFrames == nil {
		return
	}
	broadcaster := s.Broadcaster()
	if broadcaster == nil {
		broadcaster = &models.Broadcaster{}
	} else {
		copied := *broadcaster
		broadcaster = &copied
	}
	if profile != "" {
		broadcaster.StreamDetails.VideoProfile = profile
	}
	if level != "" {
		broadcaster.StreamDetails.VideoLevel = level
	}
	if width > 0 {
		broadcaster.StreamDetails.Width = width
	}
	if height > 0 {
		broadcaster.StreamDetails.Height = height
	}
	if hasBFrames != nil {
		broadcaster.StreamDetails.HasBFrames = hasBFrames
	}
	s.SetBroadcaster(broadcaster)
}
