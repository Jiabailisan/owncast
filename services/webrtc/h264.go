package webrtc

// H.264 NAL unit types used by the media path (RFC 6184 / ITU-T H.264).
const (
	nalTypeIDR   = 5
	nalTypeSPS   = 7
	nalTypePPS   = 8
	nalTypeSTAPA = 24
	nalTypeFUA   = 28
)

// h264NALTypes returns the NAL unit types carried by one H.264 RTP payload.
// Single NAL units, STAP-A aggregates and the start fragment of FU-A packets
// are understood; continuation fragments yield no type (already accounted for
// by the start fragment).
func h264NALTypes(payload []byte) []byte {
	if len(payload) == 0 {
		return nil
	}

	switch nalType := payload[0] & 0x1f; nalType {
	case nalTypeSTAPA:
		return stapANALTypes(payload)
	case nalTypeFUA:
		if len(payload) < 2 || payload[1]&0x80 == 0 {
			return nil
		}

		return []byte{payload[1] & 0x1f}
	default:
		return []byte{nalType}
	}
}

func stapANALTypes(payload []byte) []byte {
	var types []byte
	for offset := 1; offset+1 < len(payload); {
		size := int(payload[offset])<<8 | int(payload[offset+1])
		offset += 2
		if size <= 0 || offset+size > len(payload) {
			break
		}
		types = append(types, payload[offset]&0x1f)
		offset += size
	}

	return types
}

// h264ParameterSets extracts SPS and PPS NAL units from one RTP payload. Only
// single-NAL and STAP-A payloads carry parameter sets in practice; FU-A
// fragments are ignored.
func h264ParameterSets(payload []byte) (sps, pps []byte) {
	if len(payload) == 0 {
		return nil, nil
	}

	if payload[0]&0x1f == nalTypeSTAPA {
		for offset := 1; offset+1 < len(payload); {
			size := int(payload[offset])<<8 | int(payload[offset+1])
			offset += 2
			if size <= 0 || offset+size > len(payload) {
				break
			}
			nal := payload[offset : offset+size]
			offset += size
			writeParameterSet(&sps, &pps, nal)
		}

		return sps, pps
	}

	writeParameterSet(&sps, &pps, payload)

	return sps, pps
}

func writeParameterSet(sps, pps *[]byte, nal []byte) {
	if len(nal) == 0 {
		return
	}
	switch nal[0] & 0x1f {
	case nalTypeSPS:
		*sps = append([]byte(nil), nal...)
	case nalTypePPS:
		*pps = append([]byte(nil), nal...)
	}
}
