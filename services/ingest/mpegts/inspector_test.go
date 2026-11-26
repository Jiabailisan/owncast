package mpegts

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func ffmpegAvailable(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not available on PATH; skipping media integration test")
	}
	return path
}

// generateTS encodes a synthetic H.264/AAC MPEG-TS. bframes <= 0 disables
// B-frames (zerolatency), otherwise libx264 uses the default B-frame decision.
func generateTS(t *testing.T, ffmpegPath string, bframes int) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "sample.ts")
	args := []string{
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=30",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
	}
	if bframes <= 0 {
		args = append(args, "-tune", "zerolatency", "-bf", "0")
	} else {
		args = append(args, "-bf", "3")
	}
	args = append(
		args,
		"-c:v", "libx264", "-preset", "veryfast", "-profile:v", "main",
		"-g", "30", "-pix_fmt", "yuv420p", "-t", "2",
		"-c:a", "aac", "-b:a", "96k", "-shortest", "-f", "mpegts", out,
	)
	cmd := exec.Command(ffmpegPath, args...) // nolint:gosec
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg failed: %v\n%s", err, output)
	}
	return out
}

func inspectFile(t *testing.T, path string) (SourceInfo, map[uint]int) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	inspector := NewInspector()
	// Feed in small chunks to exercise cross-write packet and NAL handling.
	const chunk = 1000
	for offset := 0; offset < len(data); offset += chunk {
		end := offset + chunk
		if end > len(data) {
			end = len(data)
		}
		if _, err := inspector.Write(data[offset:end]); err != nil {
			t.Fatalf("inspector write: %v", err)
		}
	}
	return inspector.Snapshot(), inspector.SliceTypeHistogram()
}

func TestInspectorParsesH264NoBFrames(t *testing.T) {
	ffmpegPath := ffmpegAvailable(t)
	path := generateTS(t, ffmpegPath, 0)

	info, histogram := inspectFile(t, path)
	if info.VideoCodec != "h264" {
		t.Errorf("video codec = %q, want h264", info.VideoCodec)
	}
	if info.AudioCodec != "aac" {
		t.Errorf("audio codec = %q, want aac", info.AudioCodec)
	}
	if info.Width != 1280 || info.Height != 720 {
		t.Errorf("resolution = %dx%d, want 1280x720", info.Width, info.Height)
	}
	if !info.FPSValid || info.FPS < 29 || info.FPS > 31 {
		t.Errorf("fps = %v (valid=%v), want about 30", info.FPS, info.FPSValid)
	}
	if info.Profile == "" || info.Profile == "unknown" {
		t.Errorf("unexpected profile %q", info.Profile)
	}
	if !info.BFrameChecked {
		t.Fatalf("B-frame verdict not reached; histogram=%v", histogram)
	}
	if info.HasBFrames {
		t.Errorf("expected no B-frames with -tune zerolatency, histogram=%v", histogram)
	}
}

func TestInspectorDetectsBFrames(t *testing.T) {
	ffmpegPath := ffmpegAvailable(t)
	path := generateTS(t, ffmpegPath, 3)

	info, histogram := inspectFile(t, path)
	if info.VideoCodec != "h264" {
		t.Errorf("video codec = %q, want h264", info.VideoCodec)
	}
	if !info.BFrameChecked || !info.HasBFrames {
		t.Errorf("expected B-frames to be detected; checked=%v hasB=%v histogram=%v",
			info.BFrameChecked, info.HasBFrames, histogram)
	}
}

func TestFindStartCode(t *testing.T) {
	tests := []struct {
		name    string
		buf     []byte
		from    int
		wantPos int
		wantLen int
	}{
		{"three byte", []byte{0x00, 0x00, 0x01, 0x67}, 0, 0, 3},
		{"four byte", []byte{0x00, 0x00, 0x00, 0x01, 0x67}, 0, 0, 4},
		{"offset", []byte{0xff, 0x00, 0x00, 0x01}, 0, 1, 3},
		{"none", []byte{0x00, 0x00, 0x00, 0x00}, 0, -1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pos, length := findStartCode(tt.buf, tt.from)
			if pos != tt.wantPos || length != tt.wantLen {
				t.Errorf("findStartCode = (%d,%d), want (%d,%d)", pos, length, tt.wantPos, tt.wantLen)
			}
		})
	}
}

func inspectFixture(t *testing.T, path string) SourceInfo {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading fixture %s: %v", path, err)
	}
	inspector := NewInspector()
	const chunk = 1000
	for offset := 0; offset < len(data); offset += chunk {
		end := offset + chunk
		if end > len(data) {
			end = len(data)
		}
		if _, err := inspector.Write(data[offset:end]); err != nil {
			t.Fatalf("inspector write: %v", err)
		}
	}
	return inspector.Snapshot()
}

// TestInspectorBitratesAndAACDetailsFromFixture proves the media-detail parity
// work against a real MPEG-TS fixture: measured rolling bitrates for both
// streams and the AAC profile/sample-rate/channel details.
func TestInspectorBitratesAndAACDetailsFromFixture(t *testing.T) {
	info := inspectFixture(t, filepath.Join("testdata", "sample-720p30-nob-2s.ts"))

	if info.VideoCodec != "h264" || info.AudioCodec != "aac" {
		t.Fatalf("codecs = %q/%q, want h264/aac", info.VideoCodec, info.AudioCodec)
	}
	if info.Width != 1280 || info.Height != 720 {
		t.Errorf("resolution = %dx%d, want 1280x720", info.Width, info.Height)
	}

	if !info.VideoBitrateValid {
		t.Fatalf("video bitrate not valid (samples too short?); info=%+v", info)
	}
	// Source encode is -b:v 2500k -maxrate 3000k, so the measured value must
	// land in a broad but meaningful range.
	if info.VideoBitrateKbps < 1800 || info.VideoBitrateKbps > 4000 {
		t.Errorf("video bitrate = %.0f kbps, want 1800..4000", info.VideoBitrateKbps)
	}

	if !info.AudioBitrateValid {
		t.Fatalf("audio bitrate not valid; info=%+v", info)
	}
	if info.AudioBitrateKbps < 100 || info.AudioBitrateKbps > 180 {
		t.Errorf("audio bitrate = %.0f kbps, want 100..180", info.AudioBitrateKbps)
	}

	if info.AudioProfile != "LC" {
		t.Errorf("audio profile = %q, want LC", info.AudioProfile)
	}
	if info.AudioSampleRate != 48000 {
		t.Errorf("audio sample rate = %d, want 48000", info.AudioSampleRate)
	}
	if info.AudioChannels != 2 {
		t.Errorf("audio channels = %d, want 2", info.AudioChannels)
	}
	if want := "AAC-LC 48kHz stereo"; info.AudioCodecDetail != want {
		t.Errorf("audio codec detail = %q, want %q", info.AudioCodecDetail, want)
	}
}

// TestInspectorBitratesFromWorkspaceSample exercises the full 12s QA source
// when this checkout is inside the project workspace. It is skipped in CI
// where the workspace test-data directory is absent.
func TestInspectorBitratesFromWorkspaceSample(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "test-data", "sample-720p30-nob.ts")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("workspace QA sample not available: %v", err)
	}

	info := inspectFixture(t, path)
	if !info.VideoBitrateValid || info.VideoBitrateKbps < 2000 || info.VideoBitrateKbps > 3500 {
		t.Errorf("video bitrate = %.0f kbps (valid=%v), want 2000..3500", info.VideoBitrateKbps, info.VideoBitrateValid)
	}
	if !info.AudioBitrateValid || info.AudioBitrateKbps < 100 || info.AudioBitrateKbps > 180 {
		t.Errorf("audio bitrate = %.0f kbps (valid=%v), want 100..180", info.AudioBitrateKbps, info.AudioBitrateValid)
	}
	if info.AudioProfile != "LC" || info.AudioSampleRate != 48000 || info.AudioChannels != 2 {
		t.Errorf("AAC details = %q %dHz %dch", info.AudioProfile, info.AudioSampleRate, info.AudioChannels)
	}
}

func TestSourceBitrateTracker(t *testing.T) {
	var tracker sourceBitrateTracker

	if _, ok := tracker.rateKbps(); ok {
		t.Fatal("empty tracker must not report a bitrate")
	}
	tracker.add(0, 1000)
	if _, ok := tracker.rateKbps(); ok {
		t.Fatal("a single sample must not report a bitrate")
	}

	// 60 frames at 30fps, 3000 bytes per frame. Expected ~732 kbps.
	for frame := 0; frame < 60; frame++ {
		tracker.add(int64(frame)*3000, 3000)
	}
	kbps, ok := tracker.rateKbps()
	if !ok {
		t.Fatal("expected a valid bitrate after 2s of samples")
	}
	if kbps < 700 || kbps > 760 {
		t.Errorf("bitrate = %.0f kbps, want about 732", kbps)
	}
}

func TestSourceBitrateTrackerWindow(t *testing.T) {
	var tracker sourceBitrateTracker
	// 100 frames at 30fps (3.33s) of 1000 bytes. Only the last 3s are in the
	// window, so the reported rate is over ~3s of media time.
	for frame := 0; frame < 100; frame++ {
		tracker.add(int64(frame)*3000, 1000)
	}
	kbps, ok := tracker.rateKbps()
	if !ok {
		t.Fatal("expected a valid bitrate")
	}
	if kbps < 220 || kbps > 280 {
		t.Errorf("windowed bitrate = %.0f kbps, want about 242", kbps)
	}
}

func TestParseADTSHeader(t *testing.T) {
	// 0xFF 0xF1: ADTS sync + MPEG-4 + no CRC. Profile LC (1), sample rate
	// index 3 (48 kHz), channel configuration 2 (stereo), frame length 100.
	header := []byte{0xFF, 0xF1, 0x4C, 0x80, 0x0C, 0x80, 0x00}
	parsed, ok := parseADTSHeader(header)
	if !ok {
		t.Fatal("expected a valid ADTS header")
	}
	if parsed.profileName() != "LC" || parsed.sampleRate != 48000 || parsed.channels != 2 {
		t.Errorf("parsed = %+v", parsed)
	}
	if parsed.frameLength != 100 {
		t.Errorf("frame length = %d, want 100", parsed.frameLength)
	}
	if parsed.detail() != "AAC-LC 48kHz stereo" {
		t.Errorf("detail = %q", parsed.detail())
	}

	if _, ok := parseADTSHeader([]byte{0x00, 0x01}); ok {
		t.Error("short/invalid header must not parse")
	}
	if _, ok := parseADTSHeader([]byte{0xFF, 0xF1, 0x4C}); ok {
		t.Error("short header must not parse")
	}
}

func TestParsePESPayloadExtractsPTS(t *testing.T) {
	const wantedPTS = int64(90000) // one second at 90 kHz

	b0 := byte(0x21 | (wantedPTS >> 29 & 0x0E))
	b1 := byte(wantedPTS >> 22)
	b2 := byte((wantedPTS>>15)&0x7F)<<1 | 0x01
	b3 := byte((wantedPTS >> 7) & 0xFF)
	b4 := byte(wantedPTS&0x7F)<<1 | 0x01

	pes := []byte{0x00, 0x00, 0x01, 0xE0, 0x00, 0x08, 0x80, 0x80, 0x05, b0, b1, b2, b3, b4, 0xAA, 0xBB}
	payload, pts, hasPTS := parsePESPayload(pes, true)
	if !hasPTS {
		t.Fatal("expected a PTS")
	}
	if pts != wantedPTS {
		t.Errorf("PTS = %d, want %d", pts, wantedPTS)
	}
	if len(payload) != 2 || payload[0] != 0xAA || payload[1] != 0xBB {
		t.Errorf("payload = %v", payload)
	}

	// Continuation packets carry ES bytes only.
	payload, pts, hasPTS = parsePESPayload([]byte{0x01, 0x02}, false)
	if hasPTS || len(payload) != 2 {
		t.Errorf("continuation payload = %v pts=%d hasPTS=%v", payload, pts, hasPTS)
	}
}

func buildTestSPSNAL(profileIDC, constraint, levelIDC uint32) []byte {
	var bw bitWriter
	bw.writeBits(profileIDC, 8)
	bw.writeBits(constraint, 8)
	bw.writeBits(levelIDC, 8)
	bw.writeUE(0) // seq_parameter_set_id
	if profileIDC == 100 {
		bw.writeUE(1)  // chroma_format_idc = 4:2:0
		bw.writeUE(0)  // bit_depth_luma_minus8
		bw.writeUE(0)  // bit_depth_chroma_minus8
		bw.writeBit(0) // qpprime_y_zero_transform_bypass_flag
		bw.writeBit(0) // seq_scaling_matrix_present_flag
	}
	bw.writeUE(0)   // log2_max_frame_num_minus4
	bw.writeUE(0)   // pic_order_cnt_type
	bw.writeUE(0)   // log2_max_pic_order_cnt_lsb_minus4
	bw.writeUE(1)   // max_num_ref_frames
	bw.writeBit(0)  // gaps_in_frame_num_value_allowed_flag
	bw.writeUE(119) // 1920
	bw.writeUE(67)  // 1088
	bw.writeBit(1)  // frame_mbs_only_flag
	bw.writeBit(1)  // direct_8x8_inference_flag
	bw.writeBit(1)  // frame_cropping_flag
	bw.writeUE(0)   // crop_left
	bw.writeUE(0)   // crop_right
	bw.writeUE(0)   // crop_top
	bw.writeUE(4)   // crop_bottom -> 1080
	bw.writeBit(0)  // vui_parameters_present_flag

	return append([]byte{0x67}, bw.finish()...)
}

func TestH264ProbeBaselineDecidesNoBFramesOnSPS(t *testing.T) {
	tests := []struct {
		name           string
		profileIDC     uint32
		constraint     uint32
		wantProfile    string
		wantDecided    bool
		wantHasBFrames bool
	}{
		{
			name:        "baseline decides immediately",
			profileIDC:  66,
			constraint:  0x00,
			wantProfile: "baseline",
			wantDecided: true,
		},
		{
			name:        "constrained baseline decides immediately",
			profileIDC:  66,
			constraint:  0x40,
			wantProfile: "constrained-baseline",
			wantDecided: true,
		},
		{
			name:        "main waits for slices",
			profileIDC:  77,
			constraint:  0x00,
			wantProfile: "main",
			wantDecided: false,
		},
		{
			name:        "high waits for slices",
			profileIDC:  100,
			constraint:  0x00,
			wantProfile: "high",
			wantDecided: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var probe H264Probe
			probe.FeedNAL(buildTestSPSNAL(tt.profileIDC, tt.constraint, 42))

			snap := probe.Snapshot()
			if snap.Profile != tt.wantProfile {
				t.Fatalf("profile = %q, want %q", snap.Profile, tt.wantProfile)
			}
			if snap.Width != 1920 || snap.Height != 1080 {
				t.Fatalf("size = %dx%d, want 1920x1080", snap.Width, snap.Height)
			}
			if probe.Decided() != tt.wantDecided || snap.BFrameChecked != tt.wantDecided {
				t.Fatalf("decided=%v checked=%v, want %v", probe.Decided(), snap.BFrameChecked, tt.wantDecided)
			}
			if snap.HasBFrames != tt.wantHasBFrames {
				t.Fatalf("HasBFrames = %v, want %v", snap.HasBFrames, tt.wantHasBFrames)
			}
		})
	}
}

func TestH264ProbeBaselineSPSDoesNotOverrideObservedBFrame(t *testing.T) {
	var probe H264Probe
	// B-slice: first_mb_in_slice=0 ("1"), slice_type=1 ("010") -> 0b10100000 = 0xA0.
	probe.FeedNAL([]byte{0x41, 0xA0})
	probe.FeedNAL(buildTestSPSNAL(66, 0x40, 42))

	snap := probe.Snapshot()
	if !snap.BFrameChecked || !snap.HasBFrames {
		t.Fatalf("observed B-frame verdict was overwritten: checked=%v hasB=%v", snap.BFrameChecked, snap.HasBFrames)
	}
}
