package transcoder

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/owncast/owncast/config"
	"github.com/owncast/owncast/models"
)

func TestThumbnailDecodeFlags(t *testing.T) {
	if got := thumbnailDecodeFlags("h264_nvenc"); !reflect.DeepEqual(got, []string{"-hwaccel", "cuda"}) {
		t.Fatalf("nvenc decode flags = %#v, want -hwaccel cuda", got)
	}
	if got := thumbnailDecodeFlags("libx264"); got != nil {
		t.Fatalf("software codec decode flags = %#v, want nil", got)
	}
	if got := thumbnailDecodeFlags("h264_qsv"); got != nil {
		t.Fatalf("qsv decode flags = %#v, want nil until hwdownload handling exists", got)
	}
}

func TestThumbnailCommandFlagsUseConfiguredHardwareDecoder(t *testing.T) {
	flags := thumbnailCommandFlags("in.ts", "out.jpg", "h264_nvenc")
	hwaccel := indexOf(flags, "-hwaccel")
	input := indexOf(flags, "-i")
	if hwaccel < 0 || input < 0 || hwaccel > input {
		t.Fatalf("expected -hwaccel before -i, got %#v", flags)
	}
	if flags[hwaccel] != "-hwaccel" || flags[hwaccel+1] != "cuda" {
		t.Fatalf("decode flags not placed before input: %#v", flags)
	}
	if indexOf(flags, "-vframes") < 0 || flags[len(flags)-1] != "out.jpg" {
		t.Fatalf("still-frame output missing: %#v", flags)
	}

	gif := animatedGifCommandFlags("in.ts", "out.gif", "h264_nvenc")
	if indexOf(gif, "-hwaccel") < 0 || indexOf(gif, "-hwaccel") > indexOf(gif, "-i") {
		t.Fatalf("expected hardware decode before input for gif: %#v", gif)
	}
	if indexOf(gif, "-filter_complex") < 0 || gif[len(gif)-1] != "out.gif" {
		t.Fatalf("gif filter/output missing: %#v", gif)
	}

	software := thumbnailCommandFlags("in.ts", "out.jpg", "libx264")
	if indexOf(software, "-hwaccel") >= 0 {
		t.Fatalf("software codec should not add hwaccel: %#v", software)
	}
}

func indexOf(values []string, want string) int {
	for index, value := range values {
		if value == want {
			return index
		}
	}
	return -1
}

type thumbnailConfigStub struct {
	models.EngineConfig
	ffmpegPath string
	videoCodec string
}

func (c thumbnailConfigStub) GetFfMpegPath() string { return c.ffmpegPath }
func (c thumbnailConfigStub) GetVideoCodec() string { return c.videoCodec }

type errorCaptureHook struct {
	mu      sync.Mutex
	entries []string
}

func (h *errorCaptureHook) Levels() []log.Level {
	return []log.Level{log.ErrorLevel, log.FatalLevel, log.PanicLevel}
}

func (h *errorCaptureHook) Fire(entry *log.Entry) error {
	h.mu.Lock()
	h.entries = append(h.entries, entry.Message)
	h.mu.Unlock()
	return nil
}

func (h *errorCaptureHook) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.entries)
}

func TestThumbnailGeneratorSuppressesCancelErrorOnStop(t *testing.T) {
	tmpDir := t.TempDir()
	segDir := filepath.Join(tmpDir, "0")
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		t.Fatalf("mkdir segment dir: %v", err)
	}
	segFile := filepath.Join(segDir, "0001.ts")
	if err := os.WriteFile(segFile, []byte("ts"), 0o644); err != nil {
		t.Fatalf("write segment: %v", err)
	}

	// Fake ffmpeg binary that sleeps until killed by context cancellation.
	fakeFFmpeg := filepath.Join(tmpDir, "fake-ffmpeg.sh")
	if err := os.WriteFile(fakeFFmpeg, []byte("#!/bin/sh\nexec sleep 10\n"), 0o755); err != nil {
		t.Fatalf("write fake ffmpeg: %v", err)
	}

	hook := &errorCaptureHook{}
	nextHooks := make(log.LevelHooks)
	nextHooks.Add(hook)
	prevHooks := log.StandardLogger().ReplaceHooks(nextHooks)
	t.Cleanup(func() {
		log.StandardLogger().ReplaceHooks(prevHooks)
	})

	gen := NewThumbnailGenerator(
		&config.Config{TempDir: tmpDir},
		thumbnailConfigStub{ffmpegPath: fakeFFmpeg, videoCodec: "libx264"},
	)

	// 1. Cancelling parentCtx during thumbnail extraction returns nil with no error log.
	ctx1, cancel1 := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel1()
	}()
	if err := gen.fireThumbnailGenerator(ctx1, tmpDir, 0); err != nil {
		t.Fatalf("fireThumbnailGenerator returned %v after context cancel, want nil", err)
	}

	// 2. Cancelling parentCtx during animated GIF preview logs no error.
	ctx2, cancel2 := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel2()
	}()
	gen.makeAnimatedGifPreview(ctx2, segFile, filepath.Join(tmpDir, "preview.gif"))

	if got := hook.count(); got != 0 {
		t.Fatalf("expected 0 error logs on cancelled thumbnail/gif commands, got %d: %v", got, hook.entries)
	}

	// 3. A real non-cancelled ffmpeg failure must still return and log an error.
	failingFFmpeg := filepath.Join(tmpDir, "failing-ffmpeg.sh")
	if err := os.WriteFile(failingFFmpeg, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write failing ffmpeg: %v", err)
	}
	failGen := NewThumbnailGenerator(
		&config.Config{TempDir: tmpDir},
		thumbnailConfigStub{ffmpegPath: failingFFmpeg, videoCodec: "libx264"},
	)
	if err := failGen.fireThumbnailGenerator(context.Background(), tmpDir, 0); err == nil {
		t.Fatal("fireThumbnailGenerator returned nil for non-cancelled ffmpeg failure, want error")
	}
	failGen.makeAnimatedGifPreview(context.Background(), segFile, filepath.Join(tmpDir, "preview.gif"))
	if got := hook.count(); got == 0 {
		t.Fatal("expected error log for non-cancelled animated gif failure, got 0")
	}
}
