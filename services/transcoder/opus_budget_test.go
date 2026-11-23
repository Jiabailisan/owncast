package transcoder

import (
	"slices"
	"testing"
)

// The RTP muxer rejects a payload larger than pkt_size minus the RTP header and fails the whole
// output, which takes the WebRTC process down. The frame duration has to keep the worst case Opus
// packet inside that budget.
func TestOpusFrameForBudget(t *testing.T) {
	cases := []struct {
		name    string
		bitrate int
		frame   int
		want    int
	}{
		// The default: 96 kbps at 20 ms is 240 bytes nominal, 480 worst case, comfortably inside.
		{"default 96k/20ms stays", 96, 20, 20},
		{"128k/20ms stays", 128, 20, 20},
		{"192k/20ms stays", 192, 20, 20},
		// 256 kbps at 20 ms is 640 bytes nominal and 1280 worst case, over the 1188 budget.
		{"256k/20ms shortens", 256, 20, 10},
		{"256k/10ms stays", 256, 10, 10},
		{"320k/20ms shortens", 320, 20, 10},
		// A frame duration that already fits is never lengthened.
		{"10ms is not lengthened", 96, 10, 10},
		{"5ms is not lengthened", 96, 5, 5},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := opusFrameForBudget(tc.bitrate, tc.frame)
			if got != tc.want {
				t.Fatalf("opusFrameForBudget(%d, %d) = %d, want %d", tc.bitrate, tc.frame, got, tc.want)
			}
		})
	}
}

// Every combination the clamp returns must actually fit the RTP payload at the measured burst
// factor, otherwise the fix would only move the failure.
func TestOpusFrameForBudgetAlwaysFits(t *testing.T) {
	for bitrate := 32; bitrate <= 512; bitrate += 32 {
		for _, frame := range []int{2, 5, 10, 20, 40, 60} {
			got := opusFrameForBudget(bitrate, frame)
			nominal := bitrate * got / 8
			if nominal*opusVBRBurstFactor > maxOpusPayloadBytes {
				t.Fatalf("%d kbps at %d ms was clamped to %d ms, which still yields %d bytes against a %d byte budget",
					bitrate, frame, got, nominal*opusVBRBurstFactor, maxOpusPayloadBytes)
			}
		}
	}
}

// The audio command must carry the clamped duration, not the configured one.
func TestRTPAudioUsesClampedFrame(t *testing.T) {
	out := RTPOutput{
		OpusBitrateKbps:  256,
		OpusFrameMs:      20,
		AudioPayloadType: 111,
		AudioURL:         "rtp://127.0.0.1:5000",
	}
	tc := &Transcoder{}
	args := tc.rtpAudioOutputFlags(out)

	i := slices.Index(args, "-frame_duration")
	if i < 0 || i+1 >= len(args) {
		t.Fatalf("no -frame_duration in %v", args)
	}
	if args[i+1] != "10" {
		t.Fatalf("frame duration %q, want 10 for 256 kbps", args[i+1])
	}
	if j := slices.Index(args, "-b:a"); j < 0 || args[j+1] != "256k" {
		t.Fatalf("bitrate must stay at the configured value, got %v", args)
	}
}

// The shared path must clamp too: both audio paths read the same settings.
func TestSharedAudioUsesClampedFrame(t *testing.T) {
	outputs := []RTPOutput{
		{OpusBitrateKbps: 256, OpusFrameMs: 20, AudioPayloadType: 111},
		{OpusBitrateKbps: 256, OpusFrameMs: 20, AudioPayloadType: 111},
	}
	tc := &Transcoder{rtpOutputs: outputs, variants: []HLSVariant{{}, {}}}
	args := tc.sharedAudioEncoderFlags()
	if len(args) == 0 {
		t.Fatal("shared audio flags were not rendered")
	}
	joined := ""
	for _, a := range args {
		joined += a + " "
	}
	if !contains(joined, "10") {
		t.Fatalf("shared audio must carry the clamped frame duration, got %v", args)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}

	return false
}
