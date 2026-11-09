package transcoder

import (
	"strings"
	"testing"
)

// rtpTestOutput builds a transcode WebRTC leg for the given configured encoder.
func rtpTestOutput(codecName string) RTPOutput {
	return RTPOutput{
		VariantIndex:     0,
		VideoURL:         "rtp://127.0.0.1:40000?pkt_size=1200",
		AudioURL:         "rtp://127.0.0.1:40001?pkt_size=1200",
		VideoPayloadType: 96,
		AudioPayloadType: 111,
		VideoBitrateKbps: 2500,
		GOPFrames:        30,
		H264Profile:      "main",
		VideoCodec:       codecName,
		CPUUsageLevel:    4,
	}
}

// TestRTPEncoderTuningPerCodec pins the encoder-specific preset/tune flags for
// WebRTC transcode legs. A wrong tune (e.g. libx264's "zerolatency" on
// h264_nvenc) makes FFmpeg exit at startup and took the whole shared process,
// including the live publisher, down with it (observed on hardware encoding).
func TestRTPEncoderTuningPerCodec(t *testing.T) {
	cases := []struct {
		name          string
		codec         Codec
		codecName     string
		wantPreset    string
		wantTune      string
		rejectTune    string
		wantFilterArg string
	}{
		{
			name:       "libx264 keeps zerolatency",
			codec:      &Libx264Codec{},
			codecName:  "libx264",
			wantPreset: presetFast,
			wantTune:   "zerolatency",
		},
		{
			name:       "nvenc uses ll tuning",
			codec:      &NvencCodec{},
			codecName:  "h264_nvenc",
			wantPreset: "p5",
			wantTune:   "ll",
			rejectTune: "zerolatency",
		},
		{
			name:          "qsv has no tune and needs hwupload",
			codec:         &QuicksyncCodec{},
			codecName:     "h264_qsv",
			wantPreset:    "veryslow",
			wantTune:      "",
			rejectTune:    "zerolatency",
			wantFilterArg: "hwupload=extra_hw_frames=64,format=qsv",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := newGoldenTranscoder()
			tr.codec = tc.codec
			args := strings.Join(rtpTestFlags(tr, rtpTestOutput(tc.codecName)), " ")

			if !strings.Contains(args, "-c:v "+tc.codecName) {
				t.Fatalf("expected encoder %s in %s", tc.codecName, args)
			}
			if tc.wantPreset != "" && !strings.Contains(args, "-preset "+tc.wantPreset) {
				t.Fatalf("expected preset %s in %s", tc.wantPreset, args)
			}
			if tc.wantTune != "" && !strings.Contains(args, "-tune "+tc.wantTune) {
				t.Fatalf("expected tune %s in %s", tc.wantTune, args)
			}
			if tc.rejectTune != "" && strings.Contains(args, "-tune "+tc.rejectTune) {
				t.Fatalf("tune %s must not be emitted for %s: %s", tc.rejectTune, tc.codecName, args)
			}
			if tc.wantFilterArg != "" && !strings.Contains(args, tc.wantFilterArg) {
				t.Fatalf("expected filter %q in %s", tc.wantFilterArg, args)
			}
		})
	}
}

// TestRTPCopyLegHasNoEncoderFlags keeps copy legs free of encoder options.
func TestRTPCopyLegHasNoEncoderFlags(t *testing.T) {
	tr := newGoldenTranscoder()
	tr.codec = &NvencCodec{}
	output := rtpTestOutput("h264_nvenc")
	output.CopyVideo = true
	args := strings.Join(rtpTestFlags(tr, output), " ")

	if !strings.Contains(args, "-c:v copy") {
		t.Fatalf("expected copy leg: %s", args)
	}
	for _, forbidden := range []string{"-preset", "-tune", "h264_nvenc"} {
		if strings.Contains(args, forbidden) {
			t.Fatalf("copy leg must not contain %q: %s", forbidden, args)
		}
	}
}

// rtpTestFlags renders one complete leg (video + audio) for assertions.
func rtpTestFlags(tr *Transcoder, output RTPOutput) []string {
	args := tr.rtpVideoOutputFlags(output)
	return append(args, tr.rtpAudioOutputFlags(output)...)
}
