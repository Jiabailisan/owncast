package transcoder

import (
	"strings"
	"testing"
)

// TestRTPTranscodeLegScalesAndWindowsBitrate pins the two properties the
// isolated WebRTC process needs: a transcode leg must scale to the variant size
// (it decodes the raw source, so without this a 1440p source stayed 1440p at a
// 6 Mbps budget) and it must run a VBR window around the target instead of a
// fixed rate.
func TestRTPTranscodeLegScalesAndWindowsBitrate(t *testing.T) {
	output := rtpTestOutput("libx264")
	output.Scale = ScaleExpression(1920, 1080)

	tr := new(Transcoder)
	tr.codec = &Libx264Codec{}

	args := strings.Join(tr.rtpVideoOutputFlags(output), " ")

	if !strings.Contains(args, "-filter:v scale=1920:1080") {
		t.Fatalf("transcode leg does not scale to the variant size: %s", args)
	}
	if !strings.Contains(args, "-b:v 2500k") {
		t.Fatalf("missing target bitrate: %s", args)
	}
	if !strings.Contains(args, "-minrate 500k") {
		t.Fatalf("missing VBR floor: %s", args)
	}
	if !strings.Contains(args, "-maxrate 4500k") {
		t.Fatalf("missing VBR ceiling: %s", args)
	}
}

// TestRTPCopyLegKeepsSourceResolution proves a copy leg is never scaled: it
// forwards the source bitstream untouched.
func TestRTPCopyLegKeepsSourceResolution(t *testing.T) {
	output := rtpTestOutput("libx264")
	output.CopyVideo = true
	output.Scale = ScaleExpression(1920, 1080)

	tr := new(Transcoder)
	tr.codec = &Libx264Codec{}

	args := strings.Join(tr.rtpVideoOutputFlags(output), " ")
	if strings.Contains(args, "scale=") {
		t.Fatalf("copy leg must not scale: %s", args)
	}
}

// TestScaleExpression covers the aspect-ratio cases of a configured variant.
func TestScaleExpression(t *testing.T) {
	cases := []struct {
		width, height int
		want          string
	}{
		{1920, 1080, "1920:1080"},
		{1280, 0, "1280:-2"},
		{0, 720, "-2:720"},
		{0, 0, ""},
	}
	for _, testCase := range cases {
		if got := ScaleExpression(testCase.width, testCase.height); got != testCase.want {
			t.Fatalf("ScaleExpression(%d, %d) = %q, want %q", testCase.width, testCase.height, got, testCase.want)
		}
	}
}
