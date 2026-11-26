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

func TestSharedWriteMasterPlaylistPopulatesPassthroughSourceMetadata(t *testing.T) {
	origPath := config.HLSStoragePath
	tmpDir := t.TempDir()
	config.HLSStoragePath = tmpDir
	t.Cleanup(func() { config.HLSStoragePath = origPath })

	tr := &Transcoder{
		codec:               getCodec("libx264"),
		currentLatencyLevel: models.LatencyLevel{SecondsPerSegment: 2},
		variants: []HLSVariant{
			{
				index:              0,
				isVideoPassthrough: true,
				isAudioPassthrough: true,
			},
			{
				index:        1,
				videoBitrate: 6000,
				framerate:    60,
				videoSize:    VideoSize{Width: 1920, Height: 1080},
			},
		},
		rtpOutputs: []RTPOutput{
			{
				VariantIndex:     1,
				VideoURL:         "rtp://127.0.0.1:5000",
				AudioURL:         "rtp://127.0.0.1:5002",
				VideoPayloadType: 96,
				AudioPayloadType: 111,
				VideoBitrateKbps: 6000 - 192,
				GOPFrames:        120,
				Framerate:        60,
				Scale:            "1920:1080",
				H264Profile:      "main",
				OpusBitrateKbps:  128,
			},
		},
	}
	tr.SetSharedH264Profile("main")
	tr.SetSharedH264Level("4.2")
	tr.SetSourceDetails(2560, 1440, 12000, 60, "constrained-high", "5.1")

	tr.writeMasterPlaylist()

	data, err := os.ReadFile(filepath.Join(tmpDir, "stream.m3u8"))
	if err != nil {
		t.Fatalf("read master playlist: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "BANDWIDTH=12128000") {
		t.Fatalf("passthrough missing source bitrate floor in master playlist:\n%s", content)
	}
	if !strings.Contains(content, "RESOLUTION=2560x1440") {
		t.Fatalf("passthrough missing probed source resolution in master playlist:\n%s", content)
	}
	if !strings.Contains(content, "avc1.640033") {
		t.Fatalf("passthrough should keep source high L5.1 codec tag:\n%s", content)
	}
	if !strings.Contains(content, "avc1.4d002a") {
		t.Fatalf("transcoded variant should keep main L4.2 codec tag:\n%s", content)
	}
}
