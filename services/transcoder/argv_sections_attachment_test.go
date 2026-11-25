package transcoder

import (
	"strings"
	"testing"
)

// attachmentSharedArgv is the Shared-process command line from the field
// report: the HLS output, two independent RTP outputs and the pure-RTP Opus tee.
func attachmentSharedArgv() []string {
	return []string{
		"ffmpeg", "-hide_banner", "-nostats", "-loglevel", "level+info", "-progress", "pipe:1",
		"-hwaccel", "cuda", "-fflags", "+genpts", "-flags", "+cgop", "-f", "mpegts", "-i", "pipe:0",
		"-map", "v:0", "-c:v:0", "copy", "-b:v:0", "13000k", "-map", "a:0?", "-c:a:0", "copy", "-preset", "p1",
		"-map", "v:0", "-c:v:1", "h264_nvenc", "-b:v:1", "5808k", "-minrate:v:1", "3808k", "-maxrate:v:1", "7808k",
		"-g:v:1", "300", "-keyint_min:v:1", "300", "-r:v:1", "60", "-tune:v:1", "ll",
		"-map", "a:0?", "-c:a:1", "copy", "-sws_flags", "bilinear", "-filter:v:1", "scale=1920:1080", "-preset", "p5",
		"-var_stream_map", "v:0,a:0 v:1,a:1",
		"-f", "hls", "-hls_time", "5", "-hls_list_size", "5",
		"-hls_flags", "program_date_time+independent_segments+omit_endlist",
		"-segment_format_options", "mpegts_flags=mpegts_copyts=1", "-pix_fmt", "yuv420p",
		"-master_pl_name", "stream.m3u8",
		"-hls_segment_filename", "http://127.0.0.1:33963/%v/stream-_ulaHOgOR-%d.ts",
		"-max_muxing_queue_size", "400", "-method", "PUT", "http://127.0.0.1:33963/%v/stream.m3u8",
		"-map", "0:v:0", "-c:v", "copy", "-bsf:v", "h264_mp4toannexb,dump_extra=freq=keyframe",
		"-f", "rtp", "-payload_type", "96", "rtp://127.0.0.1:41505?pkt_size=1380",
		"-map", "0:v:0", "-c:v", "h264_nvenc", "-preset", "p5", "-tune", "ll", "-filter:v", "scale=1920:1080",
		"-profile:v", "high", "-bf", "0", "-g", "60", "-keyint_min", "60", "-r", "60",
		"-b:v", "6000k", "-minrate", "6000k", "-maxrate", "8000k", "-bufsize", "8000k", "-pix_fmt", "yuv420p",
		"-bsf:v", "dump_extra=freq=keyframe", "-f", "rtp", "-payload_type", "96", "rtp://127.0.0.1:35146?pkt_size=1380",
		"-map", "0:a:0?", "-c:a", "libopus", "-b:a", "256k", "-ar", "48000", "-ac", "2",
		"-application", "lowdelay", "-frame_duration", "20",
		"-f", "tee",
		"[f=rtp:payload_type=111]rtp://127.0.0.1:54877?pkt_size=1380|[f=rtp:payload_type=111]rtp://127.0.0.1:44346?pkt_size=1380",
	}
}

// attachmentIsolatedWebRTCArgv is the field's Isolated WebRTC process: two video
// RTP outputs and the pure-RTP Opus tee, no HLS output at all.
func attachmentIsolatedWebRTCArgv() []string {
	return []string{
		"ffmpeg", "-hide_banner", "-nostats", "-loglevel", "level+info", "-progress", "pipe:1",
		"-hwaccel", "cuda", "-fflags", "+genpts+nobuffer", "-analyzeduration", "1000000",
		"-probesize", "1000000", "-f", "mpegts", "-i", "pipe:0",
		"-map", "0:v:0", "-c:v", "copy", "-bsf:v", "h264_mp4toannexb,dump_extra=freq=keyframe",
		"-f", "rtp", "-payload_type", "96", "rtp://127.0.0.1:40359?pkt_size=1380",
		"-map", "0:v:0", "-c:v", "h264_nvenc", "-preset", "p5", "-tune", "ll", "-filter:v", "scale=1920:1080",
		"-profile:v", "high", "-bf", "0", "-g", "60", "-keyint_min", "60", "-r", "60",
		"-b:v", "6000k", "-minrate", "6000k", "-maxrate", "8000k", "-bufsize", "8000k", "-pix_fmt", "yuv420p",
		"-bsf:v", "dump_extra=freq=keyframe", "-f", "rtp", "-payload_type", "96", "rtp://127.0.0.1:34824?pkt_size=1380",
		"-map", "0:a:0?", "-c:a", "libopus", "-b:a", "256k", "-ar", "48000", "-ac", "2",
		"-application", "lowdelay", "-frame_duration", "20",
		"-f", "tee",
		"[f=rtp:payload_type=111]rtp://127.0.0.1:50343?pkt_size=1380|[f=rtp:payload_type=111]rtp://127.0.0.1:42672?pkt_size=1380",
	}
}

// attachmentIsolatedHLSArgv is the field's Isolated HLS process: one plain HLS
// output, no WebRTC at all.
func attachmentIsolatedHLSArgv() []string {
	return []string{
		"ffmpeg", "-hide_banner", "-nostats", "-loglevel", "level+info", "-progress", "pipe:1",
		"-hwaccel", "cuda", "-fflags", "+genpts", "-flags", "+cgop", "-f", "mpegts", "-i", "pipe:0",
		"-map", "v:0", "-c:v:0", "copy", "-b:v:0", "13000k", "-map", "a:0?", "-c:a:0", "copy", "-preset", "p1",
		"-map", "v:0", "-c:v:1", "h264_nvenc", "-b:v:1", "5808k", "-minrate:v:1", "3808k", "-maxrate:v:1", "7808k",
		"-g:v:1", "300", "-keyint_min:v:1", "300", "-r:v:1", "60", "-tune:v:1", "ll",
		"-map", "a:0?", "-c:a:1", "copy", "-sws_flags", "bilinear", "-filter:v:1", "scale=1920:1080", "-preset", "p5",
		"-var_stream_map", "v:0,a:0 v:1,a:1",
		"-f", "hls", "-hls_time", "5", "-hls_list_size", "5",
		"-hls_flags", "program_date_time+independent_segments+omit_endlist",
		"-segment_format_options", "mpegts_flags=mpegts_copyts=1", "-pix_fmt", "yuv420p",
		"-master_pl_name", "stream.m3u8",
		"-hls_segment_filename", "http://127.0.0.1:33963/%v/stream-gF4PNOgOg-%d.ts",
		"-max_muxing_queue_size", "400", "-method", "PUT", "http://127.0.0.1:33963/%v/stream.m3u8",
	}
}

func joined(list []string) string { return strings.Join(list, " ") }

// TestClassifyAttachmentSharedArgv pins the Shared shape: the HLS section ends
// at the HLS output address, the WebRTC section carries both video RTP legs and
// the complete Opus tee, and no target crosses sections.
func TestClassifyAttachmentSharedArgv(t *testing.T) {
	sections := ClassifyArgv(attachmentSharedArgv(), nil)

	hls := joined(sections.HLS)
	if !strings.HasSuffix(hls, "http://127.0.0.1:33963/%v/stream.m3u8") {
		t.Fatalf("the HLS section must end at the HLS output address: %s", hls[len(hls)-120:])
	}
	if strings.Contains(hls, "rtp://") {
		t.Fatalf("an RTP target leaked into the HLS section: %s", hls)
	}
	if strings.Contains(hls, "-f tee") {
		t.Fatalf("the pure-RTP tee must not appear in the HLS section: %s", hls)
	}
	if !strings.Contains(hls, "-f hls") || !strings.Contains(hls, "h264_nvenc") {
		t.Fatalf("the HLS section lost its output block or encoders: %s", hls)
	}

	webrtc := joined(sections.WebRTC)
	// Both video RTP legs, with their encodes.
	if !strings.Contains(webrtc, "rtp://127.0.0.1:41505?pkt_size=1380") ||
		!strings.Contains(webrtc, "rtp://127.0.0.1:35146?pkt_size=1380") {
		t.Fatalf("both video RTP targets must be in the WebRTC section: %s", webrtc)
	}
	if !strings.Contains(webrtc, "-c:v copy") || !strings.Contains(webrtc, "h264_nvenc") {
		t.Fatalf("the video leg encodes must be in the WebRTC section: %s", webrtc)
	}
	// The Opus tee, complete: encode options, muxer marker and both targets.
	if !strings.Contains(webrtc, "-c:a libopus") || !strings.Contains(webrtc, "-application lowdelay") {
		t.Fatalf("the Opus encode options must be in the WebRTC section: %s", webrtc)
	}
	if !strings.Contains(webrtc, "-f tee") {
		t.Fatalf("the tee muxer marker must be in the WebRTC section: %s", webrtc)
	}
	if !strings.Contains(webrtc, "rtp://127.0.0.1:54877?pkt_size=1380") ||
		!strings.Contains(webrtc, "rtp://127.0.0.1:44346?pkt_size=1380") {
		t.Fatalf("both tee targets must be listed separately: %s", webrtc)
	}
	if strings.Contains(webrtc, "stream.m3u8") {
		t.Fatalf("an HLS target leaked into the WebRTC section: %s", webrtc)
	}

	// Common is the global and input section only (the tee here is pure RTP, so
	// nothing is shared).
	common := joined(sections.Common)
	if !strings.Contains(common, "-i pipe:0") || strings.Contains(common, "libopus") {
		t.Fatalf("Common must be the global+input section for a pure-RTP tee: %s", common)
	}

	// Losslessness: every element of argv appears across the sections. The pure
	// RTP tee's single pipe-joined slave argument is expanded into two entries,
	// so the sections carry one element more than the argv.
	total := len(sections.Common) + len(sections.HLS) + len(sections.WebRTC)
	if want := len(attachmentSharedArgv()) + 1; total != want {
		t.Fatalf("sections carry %d elements, want %d (argv + the expanded tee slave list)", total, want)
	}
}

// TestClassifyAttachmentIsolatedArgv pins both Isolated processes: the HLS
// process shows only HLS, the WebRTC process shows only WebRTC (including the
// complete Opus tee).
func TestClassifyAttachmentIsolatedArgv(t *testing.T) {
	hlsProc := ClassifyArgv(attachmentIsolatedHLSArgv(), nil)
	hlsJoined := joined(hlsProc.HLS)
	if !strings.HasSuffix(hlsJoined, "http://127.0.0.1:33963/%v/stream.m3u8") {
		t.Fatalf("the isolated HLS process must end at the HLS address: %s", hlsJoined[len(hlsJoined)-120:])
	}
	if len(hlsProc.WebRTC) != 0 {
		t.Fatalf("the isolated HLS process must have an empty WebRTC section: %s", joined(hlsProc.WebRTC))
	}

	webrtcProc := ClassifyArgv(attachmentIsolatedWebRTCArgv(), nil)
	if len(webrtcProc.HLS) != 0 {
		t.Fatalf("the isolated WebRTC process must have an empty HLS section: %s", joined(webrtcProc.HLS))
	}
	webrtcJoined := joined(webrtcProc.WebRTC)
	if !strings.Contains(webrtcJoined, "rtp://127.0.0.1:40359?pkt_size=1380") ||
		!strings.Contains(webrtcJoined, "rtp://127.0.0.1:34824?pkt_size=1380") {
		t.Fatalf("both video RTP legs must be present: %s", webrtcJoined)
	}
	if !strings.Contains(webrtcJoined, "-c:a libopus") || !strings.Contains(webrtcJoined, "-f tee") ||
		!strings.Contains(webrtcJoined, "rtp://127.0.0.1:50343?pkt_size=1380") ||
		!strings.Contains(webrtcJoined, "rtp://127.0.0.1:42672?pkt_size=1380") {
		t.Fatalf("the Opus tee must be complete (options, marker, both targets): %s", webrtcJoined)
	}
}

// TestClassifyMixedTeeSplitsByProtocol covers the layout where one tee carries
// both protocols: its encode options and marker go to Common, and each target
// is shown under its own protocol.
func TestClassifyMixedTeeSplitsByProtocol(t *testing.T) {
	argv := sharedTeeArgv()
	sections := ClassifyArgv(argv, nil)

	common := joined(sections.Common)
	if !strings.Contains(common, "-f tee") || !strings.Contains(common, "-c:a:2 libopus") {
		t.Fatalf("a mixed tee's shared encode and marker must be in Common: %s", common)
	}

	hls := joined(sections.HLS)
	if !strings.Contains(hls, "/0/stream.m3u8") || !strings.Contains(hls, "/1/stream.m3u8") {
		t.Fatalf("both HLS targets must be listed: %s", hls)
	}
	if strings.Contains(hls, "rtp://") || strings.Contains(hls, "-f tee") {
		t.Fatalf("the HLS section must hold only its targets: %s", hls)
	}

	webrtc := joined(sections.WebRTC)
	if !strings.Contains(webrtc, "rtp://127.0.0.1:41000") || !strings.Contains(webrtc, "rtp://127.0.0.1:41001") {
		t.Fatalf("both tee RTP targets must be listed: %s", webrtc)
	}
	if strings.Contains(webrtc, "stream.m3u8") {
		t.Fatalf("an HLS target leaked into the WebRTC section: %s", webrtc)
	}
}
