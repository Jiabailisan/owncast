package stream

import (
	"testing"
	"time"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/transcoder"
)

func TestFFmpegProcessStatusFromSnapshotMetricsAndSections(t *testing.T) {
	speed := 0.98
	cpu := 42.5
	rss := int64(256 * 1024 * 1024)
	gpu := 24.0
	gpuMemory := int64(398 * 1024 * 1024)
	startedAt := time.Now().Add(-2 * time.Minute)

	snapshot := transcoder.ProcessSnapshot{
		ID:             "main",
		Purpose:        []string{"hls"},
		PID:            4242,
		StartedAt:      startedAt,
		UptimeSeconds:  120,
		RestartCount:   1,
		Running:        true,
		Speed:          &speed,
		CPUPercent:     &cpu,
		RSSBytes:       &rss,
		GPUPercent:     &gpu,
		GPUMemoryBytes: &gpuMemory,
		GPUName:        "Quadro P600",
	}
	sections := transcoder.ArgvSections{
		Common:      []string{"-hide_banner", "-progress", "pipe:1", "-i", "pipe:0"},
		HLS:         []string{"-f", "hls"},
		WebRTC:      nil,
		Environment: []string{"PATH=***"},
	}
	argv := []string{"ffmpeg", "-hide_banner", "-progress", "pipe:1", "-i", "pipe:0", "-f", "hls"}

	status := ffmpegProcessStatusFromSnapshot(snapshot, argv, sections)

	if status.Speed == nil || *status.Speed != speed {
		t.Errorf("Speed = %v", status.Speed)
	}
	if status.CPUPercent == nil || *status.CPUPercent != cpu {
		t.Errorf("CPUPercent = %v", status.CPUPercent)
	}
	if status.RSSBytes == nil || *status.RSSBytes != rss {
		t.Errorf("RSSBytes = %v", status.RSSBytes)
	}
	if status.GPUPercent == nil || *status.GPUPercent != gpu {
		t.Errorf("GPUPercent = %v", status.GPUPercent)
	}
	if status.GPUMemoryBytes == nil || *status.GPUMemoryBytes != gpuMemory {
		t.Errorf("GPUMemoryBytes = %v", status.GPUMemoryBytes)
	}
	if status.GPUName != "Quadro P600" {
		t.Errorf("GPUName = %q", status.GPUName)
	}

	if len(status.Sections) != 4 {
		t.Fatalf("sections = %v, want exactly common/hls/webrtc/environment", status.Sections)
	}
	for _, key := range []string{"common", "hls", "webrtc", "environment"} {
		if status.Sections[key] == nil {
			t.Errorf("section %q must be a non-nil array", key)
		}
	}
	if len(status.Sections["common"]) != 5 || len(status.Sections["hls"]) != 2 {
		t.Errorf("unexpected common/hls sections: %v / %v", status.Sections["common"], status.Sections["hls"])
	}
	if len(status.Sections["webrtc"]) != 0 {
		t.Errorf("webrtc section = %v, want empty", status.Sections["webrtc"])
	}
	if len(status.Sections["environment"]) != 1 {
		t.Errorf("environment section = %v", status.Sections["environment"])
	}
}

func TestFFmpegProcessStatusFromSnapshotStopped(t *testing.T) {
	snapshot := transcoder.ProcessSnapshot{
		ID:      "webRTC",
		Purpose: []string{"webrtc"},
		Running: false,
	}
	status := ffmpegProcessStatusFromSnapshot(snapshot, nil, transcoder.ArgvSections{})
	if len(status.Purpose) != 2 || status.Purpose[1] != "exited" {
		t.Errorf("stopped process purpose = %v, want exited marker", status.Purpose)
	}
	if status.Speed != nil || status.CPUPercent != nil || status.RSSBytes != nil {
		t.Errorf("stopped process must report N/A metrics: %+v", status)
	}
	if status.StartedAt.IsZero() {
		// zero start time is valid for a process that never started.
		_ = models.LiveStatus{}
	}
}
