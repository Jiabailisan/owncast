package transcoder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owncast/owncast/config"
	"github.com/owncast/owncast/models"
)

func TestVideoPassthroughWithAudioTranscodeSetsAudioBitrate(t *testing.T) {
	variant := getVariantFromConfigQuality(models.StreamOutputVariant{
		Name:               "Video Copy Audio Transcode",
		IsVideoPassthrough: true,
		IsAudioPassthrough: false,
		AudioBitrate:       128,
	}, 0)

	if !variant.isVideoPassthrough {
		t.Fatal("expected video passthrough")
	}
	if variant.isAudioPassthrough {
		t.Fatal("expected audio transcode, got audio passthrough")
	}
	if variant.audioBitrate != "128k" {
		t.Fatalf("audioBitrate = %q, want 128k", variant.audioBitrate)
	}

	flags := strings.Join(variant.getAudioQualityString(), " ")
	if !strings.Contains(flags, "-b:a:0 128k") {
		t.Fatalf("audio flags = %q, want -b:a:0 128k", flags)
	}
}

func TestSharedWriteMasterPlaylistInvokesCallbackAndUsesSharedLevel(t *testing.T) {
	origPath := config.HLSStoragePath
	tmpDir := t.TempDir()
	config.HLSStoragePath = tmpDir
	t.Cleanup(func() { config.HLSStoragePath = origPath })

	tr := &Transcoder{
		codec:               getCodec("libx264"),
		currentLatencyLevel: models.LatencyLevel{SecondsPerSegment: 2},
		variants: []HLSVariant{
			{
				index:        0,
				videoBitrate: 2500,
				framerate:    30,
				videoSize:    VideoSize{Width: 1280, Height: 720},
			},
		},
		rtpOutputs: []RTPOutput{
			{
				VariantIndex:     0,
				VideoURL:         "rtp://127.0.0.1:5000",
				AudioURL:         "rtp://127.0.0.1:5002",
				VideoPayloadType: 96,
				AudioPayloadType: 111,
				VideoBitrateKbps: 2500 - 192,
				GOPFrames:        60,
				Framerate:        30,
				Scale:            "1280:720",
				H264Profile:      "high",
				OpusBitrateKbps:  128,
			},
		},
	}
	tr.SetSharedH264Profile("high")
	tr.SetSharedH264Level("4.1")

	var callbackPath string
	tr.SetMasterPlaylistWrittenCallback(func(p string) {
		callbackPath = p
	})

	tr.writeMasterPlaylist()

	wantPath := filepath.Join(tmpDir, "stream.m3u8")
	if callbackPath != wantPath {
		t.Fatalf("callback path = %q, want %q", callbackPath, wantPath)
	}
	data, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("read master playlist: %v", err)
	}
	// High profile (64) + Level 4.1 (29 = 0x29) -> avc1.640029
	if !strings.Contains(string(data), "avc1.640029") {
		t.Fatalf("master playlist missing configured H.264 level 4.1 codec tag:\n%s", string(data))
	}
}
