package transcoder

import (
	"strings"
	"testing"
)

// sharedTeeArgv is the shared HLS+WebRTC layout as FFmpeg receives it: one tee
// output whose slaves are pipe-joined into a single argument.
func sharedTeeArgv() []string {
	return []string{
		"ffmpeg", "-hide_banner", "-nostats", "-loglevel", "level+info",
		"-progress", "pipe:1", "-fflags", "+genpts", "-flags", "+cgop",
		"-f", "mpegts", "-i", "pipe:0",
		"-map", "v:0", "-c:v:0", "copy", "-map", "a:0?",
		"-map", "v:0", "-c:v:1", "libx264", "-g:v:1", "180",
		"-filter:v:1", "scale=1920:1080",
		"-map", "0:a:0?", "-c:a:2", "libopus",
		"-f", "tee",
		"[use_fifo=1:f=hls:select='v:0,a:0']http://127.0.0.1:8927/0/stream.m3u8|" +
			"[use_fifo=1:f=hls:select='v:1,a:1']http://127.0.0.1:8927/1/stream.m3u8|" +
			"[select='v:1':f=rtp:payload_type=96]rtp://127.0.0.1:41000?pkt_size=1200|" +
			"[select='a:2':f=rtp:payload_type=111]rtp://127.0.0.1:41001?pkt_size=1200",
		"-map", "0:v:0", "-c:v", "copy", "-f", "rtp",
		"-payload_type", "96", "rtp://127.0.0.1:42000?pkt_size=1200",
	}
}

// TestClassifyArgvSharedTeeSplitsTargets is the regression test for the FFmpeg
// panel: the tee muxer puts every HLS and RTP target into one pipe-joined
// argument, so the panel used to show an empty HLS section and one unreadable
// mega-line under WebRTC.
func TestClassifyArgvSharedTeeSplitsTargets(t *testing.T) {
	argv := sharedTeeArgv()
	sections := ClassifyArgv(argv, nil)

	if len(sections.HLS) == 0 {
		t.Fatal("the HLS section is empty for a tee output")
	}
	if len(sections.WebRTC) == 0 {
		t.Fatal("the WebRTC section is empty for a tee output")
	}

	hls := strings.Join(sections.HLS, " ")
	if !strings.Contains(hls, "/0/stream.m3u8") || !strings.Contains(hls, "/1/stream.m3u8") {
		t.Fatalf("both HLS targets must be listed separately: %s", hls)
	}
	if strings.Contains(hls, "rtp://") {
		t.Fatalf("the HLS section must not contain RTP targets: %s", hls)
	}
	if !strings.Contains(hls, "-f tee") {
		t.Fatalf("the HLS section must show the tee muxer marker: %s", hls)
	}

	webrtc := strings.Join(sections.WebRTC, " ")
	if !strings.Contains(webrtc, "rtp://127.0.0.1:41000") || !strings.Contains(webrtc, "rtp://127.0.0.1:41001") {
		t.Fatalf("the tee RTP targets must be listed separately: %s", webrtc)
	}
	if !strings.Contains(webrtc, "rtp://127.0.0.1:42000") {
		t.Fatalf("the independent output after the tee must be in the WebRTC section: %s", webrtc)
	}
	if strings.Contains(webrtc, "stream.m3u8") {
		t.Fatalf("the WebRTC section must not contain HLS targets: %s", webrtc)
	}

	// The full argv is still available untouched.
	if len(argv) == 0 || !strings.Contains(strings.Join(argv, " "), "|") {
		t.Fatal("the original argv must be preserved")
	}

	// Common still ends at the input.
	if got := strings.Join(sections.Common, " "); !strings.HasSuffix(got, "-i pipe:0") {
		t.Fatalf("the common section must end at the input: %s", got)
	}
}

// TestClassifyArgvLegacyLayoutUnchanged guards the non-tee layouts.
func TestClassifyArgvLegacyLayoutUnchanged(t *testing.T) {
	argv := []string{
		"ffmpeg", "-i", "pipe:0",
		"-map", "v:0", "-c:v:0", "libx264", "-var_stream_map", "v:0,a:0",
		"-f", "hls", "http://127.0.0.1:8927/%v/stream.m3u8",
		"-map", "0:v:0", "-c:v", "libx264", "-f", "rtp", "rtp://127.0.0.1:40000",
	}
	sections := ClassifyArgv(argv, nil)

	if !strings.Contains(strings.Join(sections.HLS, " "), "-f hls") {
		t.Fatalf("legacy HLS block not classified: %v", sections.HLS)
	}
	if !strings.Contains(strings.Join(sections.WebRTC, " "), "rtp://127.0.0.1:40000") {
		t.Fatalf("legacy RTP block not classified: %v", sections.WebRTC)
	}
	if strings.Contains(strings.Join(sections.WebRTC, " "), "stream.m3u8") {
		t.Fatalf("the HLS playlist leaked into the WebRTC section: %v", sections.WebRTC)
	}
}
