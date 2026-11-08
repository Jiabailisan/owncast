package transcoder

import (
	"strings"
	"testing"

	"github.com/owncast/owncast/services/ingest"
)

// TestRTPOnlyArgv proves the isolated WebRTC process contains only the input
// and RTP outputs: no HLS variant mapping, no HLS muxer, no playlist target.
func TestRTPOnlyArgv(t *testing.T) {
	tr := newGoldenTranscoder()
	tr.SetInputFormat(ingest.MPEGTSInputFormat())
	tr.SetRTPOnly(true)
	tr.SetRTPOutputs([]RTPOutput{
		{
			VariantIndex:     0,
			VideoURL:         "rtp://127.0.0.1:40000?pkt_size=1200",
			AudioURL:         "rtp://127.0.0.1:40001?pkt_size=1200",
			VideoPayloadType: 96,
			AudioPayloadType: 111,
			VideoBitrateKbps: 2500,
			GOPFrames:        30,
			H264Profile:      "main",
		},
	})

	args := tr.getFlags().command
	joined := strings.Join(args, " ")

	if strings.Contains(joined, "-f hls") || strings.Contains(joined, "-var_stream_map") ||
		strings.Contains(joined, "-hls_segment_filename") || strings.Contains(joined, "stream.m3u8") {
		t.Fatalf("rtp-only argv must not contain HLS output flags: %v", args)
	}
	if !strings.Contains(joined, "-f rtp") || !strings.Contains(joined, "rtp://127.0.0.1:40000") ||
		!strings.Contains(joined, "rtp://127.0.0.1:40001") {
		t.Fatalf("rtp-only argv must contain the RTP legs: %v", args)
	}
	// MPEG-TS input descriptor is still applied (before -i, regardless of the
	// low-latency input flags between them).
	if !strings.Contains(joined, "-f mpegts") || !strings.Contains(joined, "-i pipe:0") {
		t.Fatalf("rtp-only argv must retain the SRT input descriptor: %v", args)
	}
	mpegtsIndex := strings.Index(joined, "-f mpegts")
	inputIndex := strings.Index(joined, "-i pipe:0")
	if mpegtsIndex < 0 || inputIndex < 0 || mpegtsIndex > inputIndex {
		t.Fatalf("container descriptor must come before -i: %v", args)
	}
	// The HLS process argv for the same settings must remain untouched.
	hls := newGoldenTranscoder()
	hls.SetInputFormat(ingest.MPEGTSInputFormat())
	hlsArgs := hls.getFlags().command
	if strings.Contains(strings.Join(hlsArgs, " "), "-f rtp") {
		t.Fatal("HLS process argv must not contain RTP outputs in isolated mode")
	}
}

// TestSharedProcessArgvHasBothOutputs confirms the default shared model keeps
// HLS first and appends the WebRTC RTP legs.
func TestSharedProcessArgvHasBothOutputs(t *testing.T) {
	tr := newGoldenTranscoder()
	tr.SetRTPOutputs([]RTPOutput{
		{VideoURL: "rtp://127.0.0.1:41000?pkt_size=1200", AudioURL: "rtp://127.0.0.1:41001?pkt_size=1200", VideoPayloadType: 96, AudioPayloadType: 111, VideoBitrateKbps: 2500, GOPFrames: 24},
	})
	joined := strings.Join(tr.getFlags().command, " ")
	hlsIndex := strings.Index(joined, "-f hls")
	rtpIndex := strings.Index(joined, "-f rtp")
	if hlsIndex < 0 || rtpIndex < 0 || rtpIndex < hlsIndex {
		t.Fatalf("shared model must render HLS first then RTP outputs: %v", joined)
	}
}
