package transcoder

import (
	"strings"
	"testing"
)

// rtpLegArgs renders one transcode leg's argv for the given encoder.
func rtpLegArgs(t *testing.T, codecName string, mutate func(*RTPOutput)) string {
	t.Helper()

	output := rtpTestOutput(codecName)
	if mutate != nil {
		mutate(&output)
	}

	tr := new(Transcoder)
	tr.codec = getCodec(codecName)

	return strings.Join(tr.rtpVideoOutputFlags(output), " ")
}

// TestRTPTranscodeLegEmitsOneFiltergraph is the regression test for the VAAPI/QSV
// legs. FFmpeg keeps only the last -filter:v for a stream, so emitting the scale
// and the hardware upload as two flags silently dropped hwupload/format=vaapi and
// the leg failed to start, taking the shared HLS process down with it.
func TestRTPTranscodeLegEmitsOneFiltergraph(t *testing.T) {
	args := rtpLegArgs(t, "h264_vaapi", func(output *RTPOutput) {
		output.Scale = ScaleExpression(1920, 1080)
	})

	if count := strings.Count(args, "-filter:v"); count != 1 {
		t.Fatalf("expected exactly one -filter:v, got %d: %s", count, args)
	}
	// The hardware scaler, the upload and the pixel format conversion, in that order.
	if !strings.Contains(args, "-filter:v scale_vaapi=1920:1080,hwupload=extra_hw_frames=64,format=vaapi") {
		t.Fatalf("hardware filtergraph is wrong: %s", args)
	}
}

// TestRTPTranscodeLegScalesToVariantSize proves the isolated WebRTC process scales
// to the variant size. It decodes the raw source, so without this a 1440p source
// stayed 1440p while being encoded at a 1080p rendition's bitrate.
func TestRTPTranscodeLegScalesToVariantSize(t *testing.T) {
	args := rtpLegArgs(t, "libx264", func(output *RTPOutput) {
		output.Scale = ScaleExpression(1920, 1080)
	})

	if !strings.Contains(args, "-filter:v scale=1920:1080") {
		t.Fatalf("transcode leg does not scale: %s", args)
	}
}

// TestRTPCopyLegHasNoFiltergraph proves a copy leg never gets a filtergraph:
// FFmpeg rejects "copy" combined with filtering.
func TestRTPCopyLegHasNoFiltergraph(t *testing.T) {
	args := rtpLegArgs(t, "libx264", func(output *RTPOutput) {
		output.CopyVideo = true
		output.Scale = "1920:1080"
	})

	if strings.Contains(args, "-filter:v") || strings.Contains(args, "scale=") {
		t.Fatalf("copy leg must not filter: %s", args)
	}
}

// TestRTPTranscodeLegKeepsBitratePinned pins the WebRTC leg to its target. Unlike
// HLS it gets no rate feedback from the browser, so a wide VBR window would arrive
// as burst loss and jitter-buffer growth rather than recovered quality.
func TestRTPTranscodeLegKeepsBitratePinned(t *testing.T) {
	args := rtpLegArgs(t, "libx264", func(output *RTPOutput) {
		output.VideoBitrateKbps = 2500
	})

	for _, want := range []string{"-b:v 2500k", "-minrate 2500k", "-maxrate 2500k", "-bufsize 2500k"} {
		if !strings.Contains(args, want) {
			t.Fatalf("missing %q in %s", want, args)
		}
	}
}

// TestBitrateDeltaStaysRelative guards the property the master playlist relies on:
// the peak never runs away from the average at low bitrates, which is what a flat
// 2000 kbps window did to a 400 kbps rendition.
func TestBitrateDeltaStaysRelative(t *testing.T) {
	for _, target := range []int{400, 800, 1200, 2500, 6000, 12000} {
		delta := bitrateDeltaKbps(target)

		if target > 0 && delta*2 > target+1 {
			t.Fatalf("target %d: window half-width %d exceeds the target", target, delta)
		}
		if delta > bitrateWindowKbps {
			t.Fatalf("target %d: half-width %d exceeds the cap", target, delta)
		}
	}

	// The wide case the operator asked for still applies at high bitrates.
	if got := bitrateDeltaKbps(6000); got != bitrateWindowKbps {
		t.Fatalf("expected the full %d kbps half-width at 6 Mbps, got %d", bitrateWindowKbps, got)
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
