package transcoder

import (
	"strings"
	"testing"
)

func TestSharedWebRTCAudioEncodedOnce(t *testing.T) {
	tr := newGoldenTranscoder()
	tr.codec = &NvencCodec{}
	first := rtpTestOutput("h264_nvenc")
	second := rtpTestOutput("h264_nvenc")
	second.VariantIndex = 1
	second.VideoURL = "rtp://127.0.0.1:41000?pkt_size=1200"
	second.AudioURL = "rtp://127.0.0.1:41001?pkt_size=1200"
	tr.SetRTPOutputs([]RTPOutput{first, second})

	joined := strings.Join(tr.appendRTPOutputs(nil), " ")
	if got := strings.Count(joined, "-c:a libopus"); got != 1 {
		t.Fatalf("expected one shared Opus encoder, got %d: %s", got, joined)
	}
	if !strings.Contains(joined, "-f tee") {
		t.Fatalf("expected tee muxer for shared audio: %s", joined)
	}
	for _, url := range []string{first.AudioURL, second.AudioURL} {
		if !strings.Contains(joined, url) {
			t.Fatalf("shared audio output missing %s: %s", url, joined)
		}
	}
	if got := strings.Count(joined, "-f rtp"); got != 2 {
		t.Fatalf("expected two video RTP outputs, got %d: %s", got, joined)
	}
}

func TestSharedWebRTCAudioFallsBackWhenSettingsDiffer(t *testing.T) {
	tr := newGoldenTranscoder()
	tr.codec = &NvencCodec{}
	first := rtpTestOutput("h264_nvenc")
	second := rtpTestOutput("h264_nvenc")
	second.AudioURL = "rtp://127.0.0.1:41003?pkt_size=1200"
	second.OpusBitrateKbps = 64 // different from the default 96
	tr.SetRTPOutputs([]RTPOutput{first, second})

	joined := strings.Join(tr.appendRTPOutputs(nil), " ")
	if got := strings.Count(joined, "-c:a libopus"); got != 2 {
		t.Fatalf("expected per-leg Opus fallback, got %d: %s", got, joined)
	}
	if strings.Contains(joined, "-f tee") {
		t.Fatalf("differing Opus settings must not share audio: %s", joined)
	}
}

func TestAppendRTPOutputsSingleLegKeepsDirectAudio(t *testing.T) {
	tr := newGoldenTranscoder()
	tr.codec = &NvencCodec{}
	output := rtpTestOutput("h264_nvenc")
	tr.SetRTPOutputs([]RTPOutput{output})

	joined := strings.Join(tr.appendRTPOutputs(nil), " ")
	if got := strings.Count(joined, "-c:a libopus"); got != 1 {
		t.Fatalf("expected one Opus encoder, got %d: %s", got, joined)
	}
	if strings.Contains(joined, "-f tee") {
		t.Fatalf("single leg should use a direct RTP audio output: %s", joined)
	}
	if !strings.Contains(joined, output.AudioURL) {
		t.Fatalf("audio URL missing: %s", joined)
	}
}

func TestCodecDecodeFlagsAreCodecScoped(t *testing.T) {
	if got := strings.Join((&NvencCodec{}).DecodeFlags(), " "); got != "-hwaccel cuda" {
		t.Fatalf("nvenc decode flags = %q", got)
	}
	for _, codec := range []Codec{&Libx264Codec{}, &QuicksyncCodec{}, &VaapiCodec{}, &VideoToolboxCodec{}, &OmxCodec{}, &Video4Linux{}} {
		if flags := codec.DecodeFlags(); len(flags) != 0 {
			t.Fatalf("%s must not emit hardware decode flags: %#v", codec.Name(), flags)
		}
	}
}
