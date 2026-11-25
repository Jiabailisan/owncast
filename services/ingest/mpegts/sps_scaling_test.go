package mpegts

import "testing"

type bitWriter struct {
	bytes []byte
	cur   byte
	bits  uint8
}

func (w *bitWriter) writeBit(b uint8) {
	w.cur = (w.cur << 1) | (b & 1)
	w.bits++
	if w.bits == 8 {
		w.bytes = append(w.bytes, w.cur)
		w.cur = 0
		w.bits = 0
	}
}

func (w *bitWriter) writeBits(val uint32, n uint8) {
	for i := int(n) - 1; i >= 0; i-- {
		w.writeBit(uint8((val >> uint(i)) & 1))
	}
}

func (w *bitWriter) writeUE(val uint32) {
	codeNum := val + 1
	length := uint8(0)
	for tmp := codeNum; tmp > 0; tmp >>= 1 {
		length++
	}
	for i := uint8(0); i < length-1; i++ {
		w.writeBit(0)
	}
	w.writeBits(codeNum, length)
}

func (w *bitWriter) finish() []byte {
	if w.bits > 0 {
		w.bytes = append(w.bytes, w.cur<<(8-w.bits))
		w.cur = 0
		w.bits = 0
	}

	return w.bytes
}

func TestParseSPSWith8x8ScalingList(t *testing.T) {
	// Build a High Profile (100) @ Level 4.0 (40) SPS NAL unit with
	// seq_scaling_matrix_present_flag=1 and an active 8x8 scaling list (i=6)
	// carrying 64 delta_scale=0 entries (each se(0) is a single 1-bit), followed
	// by 1920x1080 dimensions (120x68 MBs with crop_bottom=4).
	var bw bitWriter
	bw.writeBits(100, 8) // profile_idc = High
	bw.writeBits(0, 8)   // constraint flags
	bw.writeBits(40, 8)  // level_idc = 4.0
	bw.writeUE(0)        // seq_parameter_set_id

	bw.writeUE(1)  // chroma_format_idc = 4:2:0
	bw.writeUE(0)  // bit_depth_luma_minus8
	bw.writeUE(0)  // bit_depth_chroma_minus8
	bw.writeBit(0) // qpprime_y_zero_transform_bypass_flag
	bw.writeBit(1) // seq_scaling_matrix_present_flag = 1

	for i := 0; i < 8; i++ {
		if i == 6 {
			bw.writeBit(1) // seq_scaling_list_present_flag[6] = 1 (8x8 list)
			for j := 0; j < 64; j++ {
				bw.writeUE(0) // se(0) -> delta_scale=0, nextScale stays 8 != 0
			}
		} else {
			bw.writeBit(0)
		}
	}

	bw.writeUE(0)   // log2_max_frame_num_minus4
	bw.writeUE(0)   // pic_order_cnt_type
	bw.writeUE(0)   // log2_max_pic_order_cnt_lsb_minus4
	bw.writeUE(1)   // max_num_ref_frames
	bw.writeBit(0)  // gaps_in_frame_num_value_allowed_flag
	bw.writeUE(119) // pic_width_in_mbs_minus1 -> 120 * 16 = 1920
	bw.writeUE(67)  // pic_height_in_map_units_minus1 -> 68 * 16 = 1088
	bw.writeBit(1)  // frame_mbs_only_flag
	bw.writeBit(1)  // direct_8x8_inference_flag
	bw.writeBit(1)  // frame_cropping_flag
	bw.writeUE(0)   // crop_left
	bw.writeUE(0)   // crop_right
	bw.writeUE(0)   // crop_top
	bw.writeUE(4)   // crop_bottom -> 1088 - 2*4 = 1080
	bw.writeBit(0)  // vui_parameters_present_flag

	info, ok := ParseSPS(bw.finish())
	if !ok {
		t.Fatal("ParseSPS failed on High Profile SPS with 8x8 scaling list")
	}
	if info.ProfileIDC != 100 || info.LevelIDC != 40 {
		t.Fatalf("profile/level = %d/%d, want 100/40", info.ProfileIDC, info.LevelIDC)
	}
	if info.Width != 1920 || info.Height != 1080 {
		t.Fatalf("dimensions = %dx%d, want 1920x1080", info.Width, info.Height)
	}
}
