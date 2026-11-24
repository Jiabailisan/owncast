package flv

import (
	"encoding/hex"
	"testing"
)

// spsHigh720p31 is a real H.264 SPS from an x264 720p High@3.1 encode. Using a
// real SPS keeps the probe on the same path production takes.
const spsHigh720p31 = "6764001facd9405005bb0110000003001000000303c0f1831960"

// ppsHigh720p31 is the matching PPS.
const ppsHigh720p31 = "68efbcb0"

func mustHex(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(value)
	if err != nil {
		t.Fatalf("decoding %q: %v", value, err)
	}

	return decoded
}

// avcConfigurationRecord builds an AVCDecoderConfigurationRecord carrying one
// SPS and one PPS.
func avcConfigurationRecord(t *testing.T) []byte {
	t.Helper()
	sps := mustHex(t, spsHigh720p31)
	pps := mustHex(t, ppsHigh720p31)

	record := []byte{1, sps[1], sps[2], sps[3], 0xFF, 0xE1}
	record = append(record, byte(len(sps)>>8), byte(len(sps)))
	record = append(record, sps...)
	record = append(record, 1, byte(len(pps)>>8), byte(len(pps)))
	record = append(record, pps...)

	return record
}

// avccNALUs wraps NAL units in the 4-byte length prefix an AVC access unit uses.
func avccNALUs(nalus ...[]byte) []byte {
	payload := make([]byte, 0, 64)
	for _, nalu := range nalus {
		length := len(nalu)
		payload = append(payload, byte(length>>24), byte(length>>16), byte(length>>8), byte(length))
		payload = append(payload, nalu...)
	}

	return payload
}

// sliceNAL builds a coded-slice NAL whose slice_type is the given value.
func sliceNAL(sliceType int) []byte {
	// first_mb_in_slice = ue(0) = "1", then slice_type as an Exp-Golomb code.
	codes := map[int]string{0: "1", 1: "010", 2: "011", 3: "00100", 4: "00101"}
	bits := "1" + codes[sliceType]
	for len(bits)%8 != 0 {
		bits += "0"
	}
	var first byte
	for index := 0; index < 8; index++ {
		first <<= 1
		if bits[index] == '1' {
			first |= 1
		}
	}

	return []byte{0x41, first}
}

func TestInspectorReadsAVCConfigurationRecord(t *testing.T) {
	inspector := NewInspector()
	inspector.FeedVideo(VideoFormatAVC, AVCPacketSequenceHeader, avcConfigurationRecord(t), 0)

	info := inspector.Snapshot()
	if info.VideoCodec != "h264" {
		t.Fatalf("video codec = %q, want h264", info.VideoCodec)
	}
	if info.Profile != "high" {
		t.Fatalf("profile = %q, want high", info.Profile)
	}
	if info.Level != "3.1" {
		t.Fatalf("level = %q, want 3.1", info.Level)
	}
	if info.Width != 1280 || info.Height != 720 {
		t.Fatalf("size = %dx%d, want 1280x720", info.Width, info.Height)
	}
	if !info.FPSValid || info.FPS < 29 || info.FPS > 31 {
		t.Fatalf("fps = %v (valid %t), want about 30", info.FPS, info.FPSValid)
	}
	if info.BFrameChecked {
		t.Fatal("the configuration record alone must not decide the B-frame verdict")
	}
}

func TestInspectorDetectsBFrames(t *testing.T) {
	inspector := NewInspector()
	inspector.FeedVideo(VideoFormatAVC, AVCPacketSequenceHeader, avcConfigurationRecord(t), 0)
	inspector.FeedVideo(VideoFormatAVC, AVCPacketNALU, avccNALUs(sliceNAL(2)), 0)
	inspector.FeedVideo(VideoFormatAVC, AVCPacketNALU, avccNALUs(sliceNAL(1)), 33)

	info := inspector.Snapshot()
	if !info.BFrameChecked || !info.HasBFrames {
		t.Fatalf("B-frames not detected: checked=%t hasB=%t", info.BFrameChecked, info.HasBFrames)
	}
	if !inspector.Decided() {
		t.Fatal("a B slice must settle the verdict immediately")
	}
}

func TestInspectorDeclaresNoBFramesAfterEnoughSlices(t *testing.T) {
	inspector := NewInspector()
	inspector.FeedVideo(VideoFormatAVC, AVCPacketSequenceHeader, avcConfigurationRecord(t), 0)
	for index := 0; index < 59; index++ {
		inspector.FeedVideo(VideoFormatAVC, AVCPacketNALU, avccNALUs(sliceNAL(0)), uint32(index*33))
	}
	if inspector.Decided() {
		t.Fatal("59 slices are not enough to declare a no-B-frame stream")
	}
	inspector.FeedVideo(VideoFormatAVC, AVCPacketNALU, avccNALUs(sliceNAL(0)), 60*33)

	info := inspector.Snapshot()
	if !info.BFrameChecked || info.HasBFrames {
		t.Fatalf("expected a no-B-frame verdict, got checked=%t hasB=%t", info.BFrameChecked, info.HasBFrames)
	}
}

func TestInspectorMeasuresVideoBitrate(t *testing.T) {
	inspector := NewInspector()
	inspector.FeedVideo(VideoFormatAVC, AVCPacketSequenceHeader, avcConfigurationRecord(t), 0)

	// 1000 bytes of payload every 100 ms for two seconds: 80 kbit/s.
	payload := avccNALUs(sliceNAL(0))
	payload = append(payload, make([]byte, 1000)...)
	for index := 0; index < 21; index++ {
		inspector.FeedVideo(VideoFormatAVC, AVCPacketNALU, payload, uint32(index*100))
	}

	info := inspector.Snapshot()
	if !info.VideoBitrateValid {
		t.Fatal("the bitrate window did not become valid after two seconds of samples")
	}
	if info.VideoBitrateKbps < 70 || info.VideoBitrateKbps > 90 {
		t.Fatalf("bitrate = %.1f kbps, want about 80", info.VideoBitrateKbps)
	}
}

func TestInspectorKeepsBitrateUnknownWithoutEnoughSamples(t *testing.T) {
	inspector := NewInspector()
	inspector.FeedVideo(VideoFormatAVC, AVCPacketNALU, avccNALUs(sliceNAL(0)), 0)
	inspector.FeedVideo(VideoFormatAVC, AVCPacketNALU, avccNALUs(sliceNAL(0)), 200)

	info := inspector.Snapshot()
	if info.VideoBitrateValid {
		t.Fatalf("bitrate reported valid (%.1f kbps) from a 200 ms span", info.VideoBitrateKbps)
	}
}

func TestInspectorParsesAACConfiguration(t *testing.T) {
	inspector := NewInspector()
	// audioObjectType 2 (LC), samplingFrequencyIndex 3 (48000), channelConfig 2.
	inspector.FeedAudio(SoundFormatAAC, AACPacketSequenceHead, []byte{0x11, 0x90}, 0)

	info := inspector.Snapshot()
	if info.AudioCodec != "aac" {
		t.Fatalf("audio codec = %q, want aac", info.AudioCodec)
	}
	if info.AudioSampleRate != 48000 || info.AudioChannels != 2 || info.AudioProfile != "LC" {
		t.Fatalf("audio config = %d Hz, %d ch, %q", info.AudioSampleRate, info.AudioChannels, info.AudioProfile)
	}
}

func TestInspectorAcceptsAnnexBPayload(t *testing.T) {
	inspector := NewInspector()
	inspector.FeedVideo(VideoFormatAVC, AVCPacketSequenceHeader, avcConfigurationRecord(t), 0)
	// A publisher that sends start codes instead of length prefixes must still
	// be inspected: 00 00 00 01 + B slice.
	inspector.FeedVideo(VideoFormatAVC, AVCPacketNALU, []byte{0, 0, 0, 1, 0x41, 0xA0}, 0)

	info := inspector.Snapshot()
	if !info.BFrameChecked || !info.HasBFrames {
		t.Fatalf("Annex-B payload not inspected: checked=%t hasB=%t", info.BFrameChecked, info.HasBFrames)
	}
}
