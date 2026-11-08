package mpegts

// SPSInfo is the subset of an H.264 sequence parameter set needed for the
// live status source details and for WebRTC copy eligibility.
type SPSInfo struct {
	ProfileIDC    uint
	Constraint    uint
	LevelIDC      uint
	ChromaFormat  uint
	Width         int
	Height        int
	HasFrameMBs   bool // frame_mbs_only_flag
	FPS           float64
	FPSValid      bool
}

// profileName returns the human-readable H.264 profile name.
func profileName(profileIDC uint, constraint uint) string {
	// constraint_set1_flag == 0x40 selects Constrained Baseline for idc 66.
	switch profileIDC {
	case 66:
		if constraint&0x40 != 0 {
			return "constrained-baseline"
		}
		return "baseline"
	case 77:
		return "main"
	case 88:
		return "extended"
	case 100:
		if constraint&0x0c != 0 {
			return "constrained-high"
		}
		return "high"
	case 110:
		return "high-10"
	case 122:
		return "high-4:2:2"
	case 244:
		return "high-4:4:4"
	default:
		return "unknown"
	}
}

// ParseSPS decodes the fields we need from a raw H.264 SPS NAL unit (the NAL
// unit type 7 payload, without start code and without the NAL header byte).
func ParseSPS(nal []byte) (*SPSInfo, bool) {
	r := newBitReader(nal)

	profileIDC, err := r.readBits(8)
	if err != nil {
		return nil, false
	}
	constraint, err := r.readBits(8)
	if err != nil {
		return nil, false
	}
	levelIDC, err := r.readBits(8)
	if err != nil {
		return nil, false
	}
	if _, err := r.readUE(); err != nil { // seq_parameter_set_id
		return nil, false
	}

	info := &SPSInfo{ProfileIDC: profileIDC, Constraint: constraint, LevelIDC: levelIDC, ChromaFormat: 1}

	switch profileIDC {
	case 100, 110, 122, 244, 44, 83, 86, 118, 128, 138, 139, 134, 135:
		chromaFormatIDC, err := r.readUE()
		if err != nil {
			return nil, false
		}
		info.ChromaFormat = chromaFormatIDC
		if chromaFormatIDC == 3 {
			if err := r.skipBits(1); err != nil { // separate_colour_plane_flag
				return nil, false
			}
		}
		if err := skipUE(r); err != nil { // bit_depth_luma_minus8
			return nil, false
		}
		if err := skipUE(r); err != nil { // bit_depth_chroma_minus8
			return nil, false
		}
		if err := r.skipBits(1); err != nil { // qpprime_y_zero_transform_bypass_flag
			return nil, false
		}
		scalingMatrix, err := r.readBit()
		if err != nil {
			return nil, false
		}
		if scalingMatrix == 1 {
			count := 8
			if chromaFormatIDC == 3 {
				count = 12
			}
			for i := 0; i < count; i++ {
				present, err := r.readBit()
				if err != nil {
					return nil, false
				}
				if present == 1 {
					if err := skipScalingList(r, 16); err != nil {
						return nil, false
					}
				}
			}
		}
	}

	if err := skipUE(r); err != nil { // log2_max_frame_num_minus4
		return nil, false
	}
	pocType, err := r.readUE()
	if err != nil {
		return nil, false
	}
	switch pocType {
	case 0:
		if err := skipUE(r); err != nil { // log2_max_pic_order_cnt_lsb_minus4
			return nil, false
		}
	case 1:
		if err := r.skipBits(1); err != nil { // delta_pic_order_always_zero_flag
			return nil, false
		}
		if _, err := r.readSE(); err != nil { // offset_for_non_ref_pic
			return nil, false
		}
		if _, err := r.readSE(); err != nil { // offset_for_top_to_bottom_field
			return nil, false
		}
		numRefFrames, err := r.readUE()
		if err != nil {
			return nil, false
		}
		for i := uint(0); i < numRefFrames; i++ {
			if _, err := r.readSE(); err != nil {
				return nil, false
			}
		}
	}

	if err := skipUE(r); err != nil { // max_num_ref_frames
		return nil, false
	}
	if err := r.skipBits(1); err != nil { // gaps_in_frame_num_value_allowed_flag
		return nil, false
	}

	widthMbsMinus1, err := r.readUE()
	if err != nil {
		return nil, false
	}
	heightMapUnitsMinus1, err := r.readUE()
	if err != nil {
		return nil, false
	}
	frameMbsOnly, err := r.readBit()
	if err != nil {
		return nil, false
	}
	info.HasFrameMBs = frameMbsOnly == 1
	if frameMbsOnly == 0 {
		if err := r.skipBits(1); err != nil { // mb_adaptive_frame_field_flag
			return nil, false
		}
	}
	if err := r.skipBits(1); err != nil { // direct_8x8_inference_flag
		return nil, false
	}

	cropLeft, cropRight, cropTop, cropBottom := uint(0), uint(0), uint(0), uint(0)
	cropping, err := r.readBit()
	if err != nil {
		return nil, false
	}
	if cropping == 1 {
		if cropLeft, err = r.readUE(); err != nil {
			return nil, false
		}
		if cropRight, err = r.readUE(); err != nil {
			return nil, false
		}
		if cropTop, err = r.readUE(); err != nil {
			return nil, false
		}
		if cropBottom, err = r.readUE(); err != nil {
			return nil, false
		}
	}

	// Cropping unit factors for the common chroma formats (H.264 7.4.2.1.1).
	subWidthC, subHeightC := uint(2), uint(2)
	switch info.ChromaFormat {
	case 1: // 4:2:0
		subWidthC, subHeightC = 2, 2
	case 2: // 4:2:2
		subWidthC, subHeightC = 2, 1
	case 3: // 4:4:4
		subWidthC, subHeightC = 1, 1
	default:
		subWidthC, subHeightC = 1, 1
	}
	frameHeightFactor := uint(2) - frameMbsOnly

	width := int((widthMbsMinus1+1)*16 - subWidthC*(cropLeft+cropRight))
	height := int((2-frameMbsOnly)*(heightMapUnitsMinus1+1)*16 - subHeightC*frameHeightFactor*(cropTop+cropBottom))
	if width <= 0 || height <= 0 {
		return nil, false
	}
	info.Width = width
	info.Height = height

	vuiPresent, err := r.readBit()
	if err != nil || vuiPresent == 0 {
		return info, true
	}
	// VUI parsing is best-effort: only the timing info is needed for FPS, but
	// it comes after several optional blocks, so each is guarded.
	if err := skipVUI(r, info); err != nil {
		return info, true
	}
	return info, true
}

func skipUE(r *bitReader) error {
	_, err := r.readUE()
	return err
}

func skipScalingList(r *bitReader, size int) error {
	lastScale := int64(8)
	nextScale := int64(8)
	for j := 0; j < size; j++ {
		if nextScale != 0 {
			deltaScale, err := r.readSE()
			if err != nil {
				return err
			}
			nextScale = (lastScale + int64(deltaScale) + 256) % 256
		}
		if nextScale != 0 {
			lastScale = nextScale
		}
	}
	return nil
}

func skipVUI(r *bitReader, info *SPSInfo) error {
	if flag, err := r.readBit(); err != nil {
		return err
	} else if flag == 1 { // aspect_ratio_info_present_flag
		idc, err := r.readBits(8)
		if err != nil {
			return err
		}
		if idc == 255 {
			if err := r.skipBits(32); err != nil { // sar_width + sar_height
				return err
			}
		}
	}
	if flag, err := r.readBit(); err != nil { // overscan_info_present_flag
		return err
	} else if flag == 1 {
		if err := r.skipBits(1); err != nil {
			return err
		}
	}
	if flag, err := r.readBit(); err != nil { // video_signal_type_present_flag
		return err
	} else if flag == 1 {
		if err := r.skipBits(4); err != nil { // video_format + full_range
			return err
		}
		if colour, err := r.readBit(); err != nil {
			return err
		} else if colour == 1 {
			if err := r.skipBits(24); err != nil { // primaries + transfer + matrix
				return err
			}
		}
	}
	if flag, err := r.readBit(); err != nil { // chroma_loc_info_present_flag
		return err
	} else if flag == 1 {
		if err := skipUE(r); err != nil {
			return err
		}
		if err := skipUE(r); err != nil {
			return err
		}
	}
	timing, err := r.readBit()
	if err != nil {
		return err
	}
	if timing == 1 {
		numUnitsInTick, err := r.readBits(32)
		if err != nil {
			return err
		}
		timeScale, err := r.readBits(32)
		if err != nil {
			return err
		}
		if numUnitsInTick > 0 {
			info.FPS = float64(timeScale) / (2 * float64(numUnitsInTick))
			info.FPSValid = info.FPS > 0
		}
	}
	return nil
}

// ProfileName returns the human-readable profile for the parsed SPS.
func (s *SPSInfo) ProfileName() string { return profileName(s.ProfileIDC, s.Constraint) }

// Level returns the level in "major.minor" H.264 form, e.g. "4.0" or "3.1".
func (s *SPSInfo) Level() string {
	major := s.LevelIDC / 10
	minor := s.LevelIDC % 10
	return string(rune('0'+major)) + "." + string(rune('0'+minor))
}
