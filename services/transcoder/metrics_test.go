package transcoder

import (
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// argvMultiset returns a sorted copy of argv so tests can prove a
// classification is lossless (every element appears exactly once).
func argvMultiset(argv []string) []string {
	copied := append([]string(nil), argv...)
	sort.Strings(copied)
	return copied
}

func classifiedArgv(sections ArgvSections) []string {
	combined := make([]string, 0, len(sections.Common)+len(sections.HLS)+len(sections.WebRTC))
	combined = append(combined, sections.Common...)
	combined = append(combined, sections.HLS...)
	combined = append(combined, sections.WebRTC...)
	return combined
}

func containsArgs(section []string, wants ...string) bool {
	for _, want := range wants {
		found := false
		for _, arg := range section {
			if arg == want {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func testRTPOutput() RTPOutput {
	return RTPOutput{
		VariantIndex:     0,
		VideoURL:         "rtp://127.0.0.1:40000?pkt_size=1200",
		AudioURL:         "rtp://127.0.0.1:40001?pkt_size=1200",
		VideoPayloadType: 96,
		AudioPayloadType: 111,
		VideoBitrateKbps: 1200,
		GOPFrames:        24,
		CopyVideo:        true,
		CPUUsageLevel:    2,
	}
}

func TestClassifyArgvSharedProcess(t *testing.T) {
	tr := newGoldenTranscoder()
	tr.SetRTPOutputs([]RTPOutput{testRTPOutput()})
	argv := tr.getFlags().command

	sections := ClassifyArgv(argv, []string{"PATH=***"})

	// Lossless: every argv element appears exactly once across the groups.
	if got := argvMultiset(classifiedArgv(sections)); !reflect.DeepEqual(got, argvMultiset(argv)) {
		t.Fatalf("classification is not lossless:\n classified: %q\n argv: %q", classifiedArgv(sections), argv)
	}

	if !containsArgs(sections.Common, "-hide_banner", "-nostats", "-progress", "pipe:1", "-i", "pipe:0") {
		t.Errorf("common section missing global/input flags: %q", sections.Common)
	}
	if !containsArgs(sections.HLS, "-var_stream_map", "-f", "hls", "-method", "PUT") {
		t.Errorf("hls section missing HLS output flags: %q", sections.HLS)
	}
	hlsTarget := false
	for _, arg := range sections.HLS {
		if strings.HasPrefix(arg, "http://") && strings.HasSuffix(arg, "stream.m3u8") {
			hlsTarget = true
		}
	}
	if !hlsTarget {
		t.Errorf("hls section missing the playlist target URL: %q", sections.HLS)
	}
	if !containsArgs(sections.WebRTC, "-map", "0:v:0", "rtp://127.0.0.1:40000?pkt_size=1200", "rtp://127.0.0.1:40001?pkt_size=1200") {
		t.Errorf("webrtc section missing RTP output flags: %q", sections.WebRTC)
	}
	if !reflect.DeepEqual(sections.Environment, []string{"PATH=***"}) {
		t.Errorf("environment = %q", sections.Environment)
	}
}

func TestClassifyArgvIsolatedWebRTCOutput(t *testing.T) {
	tr := newGoldenTranscoder()
	tr.SetRTPOnly(true)
	tr.SetRTPOutputs([]RTPOutput{testRTPOutput()})
	argv := tr.getFlags().command

	sections := ClassifyArgv(argv, nil)

	if got := argvMultiset(classifiedArgv(sections)); !reflect.DeepEqual(got, argvMultiset(argv)) {
		t.Fatalf("classification is not lossless: %q", classifiedArgv(sections))
	}
	if len(sections.HLS) != 0 {
		t.Errorf("isolated WebRTC process must have no HLS section: %q", sections.HLS)
	}
	if !containsArgs(sections.WebRTC, "-f", "rtp", "rtp://127.0.0.1:40000?pkt_size=1200") {
		t.Errorf("webrtc section missing RTP output: %q", sections.WebRTC)
	}
	if !containsArgs(sections.Common, "-progress", "pipe:1", "-i", "pipe:0") {
		t.Errorf("common section missing input flags: %q", sections.Common)
	}
}

func TestClassifyArgvHLSOnlyProcess(t *testing.T) {
	tr := newGoldenTranscoder()
	argv := tr.getFlags().command
	sections := ClassifyArgv(argv, nil)

	if len(sections.WebRTC) != 0 {
		t.Errorf("HLS-only process must have an empty webrtc section: %q", sections.WebRTC)
	}
	if got := argvMultiset(classifiedArgv(sections)); !reflect.DeepEqual(got, argvMultiset(argv)) {
		t.Fatalf("classification is not lossless: %q", classifiedArgv(sections))
	}
}

func TestProgressLineParsing(t *testing.T) {
	metrics := &processMetrics{}

	metrics.handleProgressLine("speed=0.98x")
	snapshot := metrics.snapshot()
	if snapshot.Speed == nil || *snapshot.Speed != 0.98 {
		t.Fatalf("speed = %v, want 0.98", snapshot.Speed)
	}

	metrics.handleProgressLine("speed=1.05x")
	snapshot = metrics.snapshot()
	if snapshot.Speed == nil || *snapshot.Speed != 1.05 {
		t.Fatalf("speed = %v, want 1.05", snapshot.Speed)
	}

	metrics.handleProgressLine("frame=1234")
	metrics.handleProgressLine("progress=continue")
	metrics.handleProgressLine("speed=N/A")
	metrics.handleProgressLine("not-a-progress-line")
	if snapshot = metrics.snapshot(); snapshot.Speed == nil || *snapshot.Speed != 1.05 {
		t.Fatalf("non-speed lines changed the speed: %v", snapshot.Speed)
	}
}

func TestParseNvidiaSMIOutput(t *testing.T) {
	name, utilization, memoryBytes, ok := parseNvidiaGPUStats("Quadro P600, 24, 401\n")
	if !ok {
		t.Fatal("expected nvidia-smi gpu line to parse")
	}
	if name != "Quadro P600" || utilization != 24 {
		t.Errorf("name/utilization = %q/%v", name, utilization)
	}
	if want := int64(401 * 1024 * 1024); memoryBytes != want {
		t.Errorf("gpu memory = %d, want %d", memoryBytes, want)
	}

	if _, _, _, ok := parseNvidiaGPUStats(""); ok {
		t.Error("empty nvidia-smi output must not parse")
	}
	if _, _, _, ok := parseNvidiaGPUStats("not nvidia output"); ok {
		t.Error("garbage nvidia-smi output must not parse")
	}
}

func TestParseNvidiaComputeApps(t *testing.T) {
	output := "111, 12\n22517, 398\n"
	memoryBytes, ok := parseNvidiaComputeApps(output, 22517)
	if !ok {
		t.Fatal("expected the ffmpeg pid to match a compute app")
	}
	if want := int64(398 * 1024 * 1024); memoryBytes != want {
		t.Errorf("compute app memory = %d, want %d", memoryBytes, want)
	}
	if _, ok := parseNvidiaComputeApps(output, 999); ok {
		t.Error("unknown pid must not match")
	}
	if _, ok := parseNvidiaComputeApps("garbage", 22517); ok {
		t.Error("garbage output must not match")
	}
}

func TestNvidiaSMIEncoderDetection(t *testing.T) {
	for _, encoder := range []string{"h264_nvenc", "hevc_nvenc", "H264_NVENC"} {
		if !isNvidiaSMIEncoder(encoder) {
			t.Errorf("%q should use nvidia-smi GPU sampling", encoder)
		}
	}
	for _, encoder := range []string{"libx264", "h264_qsv", "h264_vaapi", "h264_videotoolbox", ""} {
		if isNvidiaSMIEncoder(encoder) {
			t.Errorf("%q should not use nvidia-smi GPU sampling", encoder)
		}
	}
}

func TestReadProcessStatsCurrentProcess(t *testing.T) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("no /proc on this platform")
	}
	ticks, rssBytes, ok := readProcessStats(os.Getpid())
	if !ok {
		t.Fatal("expected /proc stats for the current process")
	}
	_ = ticks
	if rssBytes <= 0 {
		t.Errorf("expected positive RSS for the current process, got %d", rssBytes)
	}
}
