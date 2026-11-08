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
