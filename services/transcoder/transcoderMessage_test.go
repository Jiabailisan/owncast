package transcoder

import (
	"testing"

	log "github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
)

// TestSurfaceableTranscoderMessage proves the stderr classification contract
// under ffmpeg's `-loglevel level+info`: diagnostic levels are suppressed,
// problem levels are surfaced with only the level tag (and its trailing
// space) removed so errorMap/ignoredErrors substring matching still sees the
// original message text, and rare untagged lines pass through verbatim.
func TestSurfaceableTranscoderMessage(t *testing.T) {
	cases := []struct {
		name   string
		line   string
		want   string
		wantOK bool
	}{
		{
			"plain info suppressed",
			"[info] Stream mapping:",
			"", false,
		},
		{
			"component-prefixed info suppressed",
			"[hls @ 0x55d3] [info] Opening '/x/stream0.ts' for writing",
			"", false,
		},
		{
			"warning surfaced with tag stripped",
			"[warning] VBV underflow (frame 12, -224 bits)",
			"VBV underflow (frame 12, -224 bits)", true,
		},
		{
			"component-prefixed error keeps component tag",
			"[flv @ 0x1] [error] Failed to open file 'http://127.0.0.1'",
			"[flv @ 0x1] Failed to open file 'http://127.0.0.1'", true,
		},
		{
			"untagged line surfaced verbatim",
			"Press [q] to stop, [?] for help",
			"Press [q] to stop, [?] for help", true,
		},
		{
			"fatal surfaced stripped",
			"[fatal] Unable to find a suitable output format for 'pipe:'",
			"Unable to find a suitable output format for 'pipe:'", true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := surfaceableTranscoderMessage(tc.line)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("surfaceableTranscoderMessage(%q) = (%q, %v), want (%q, %v)", tc.line, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestClassifyAndThrottleTranscoderMessages(t *testing.T) {
	msg, isWarn, ok := classifyTranscoderMessage("[in#0/mpegts @ 0x562f715e1540] [warning] Packet corrupt (stream = 0, dts = 634920).")
	if !ok || !isWarn || msg != "[in#0/mpegts @ 0x562f715e1540] Packet corrupt (stream = 0, dts = 634920)." {
		t.Fatalf("classify warning = (%q, %v, %v)", msg, isWarn, ok)
	}

	msgErr, isWarnErr, okErr := classifyTranscoderMessage("[h264 @ 0x562f72261380] [error] illegal short term buffer state detected")
	if !okErr || isWarnErr || msgErr != "[h264 @ 0x562f72261380] illegal short term buffer state detected" {
		t.Fatalf("classify error = (%q, %v, %v)", msgErr, isWarnErr, okErr)
	}

	hook := test.NewGlobal()
	t.Cleanup(hook.Reset)

	tr := &Transcoder{}
	tr.handleTranscoderMessageWithLevel("[in#0/mpegts @ 0x562f715e1540] Packet corrupt (stream = 0, dts = 634920).", true)
	tr.handleTranscoderMessageWithLevel("[h264 @ 0x562f72261380] illegal short term buffer state detected", false)
	// Second burst with different hex pointer and DTS within cooldown window should be throttled.
	tr.handleTranscoderMessageWithLevel("[in#0/mpegts @ 0x55b7925df740] Packet corrupt (stream = 0, dts = 700000).", true)
	tr.handleTranscoderMessageWithLevel("[h264 @ 0x55b79378b6c0] illegal short term buffer state detected", false)

	var warnCount, errCount int
	for _, entry := range hook.AllEntries() {
		switch entry.Level {
		case log.WarnLevel:
			warnCount++
		case log.ErrorLevel:
			errCount++
		}
	}
	if warnCount != 1 || errCount != 1 {
		t.Fatalf("expected 1 warn and 1 error after throttling alternating burst, got warn=%d error=%d", warnCount, errCount)
	}
}

func TestTranscoderLogClassifierInheritsMultiLineLevel(t *testing.T) {
	var c transcoderLogClassifier

	msg, isWarn, ok := c.classify("[in#0/flv @ 0x55e9072fbb00] [warning] Could not find codec parameters for stream 1 (Video: h264, none, 6144 kb/s): unspecified size")
	if !ok || !isWarn || msg == "" {
		t.Fatalf("first warning line = (%q, %v, %v)", msg, isWarn, ok)
	}
	cont, contWarn, contOK := c.classify("Consider increasing the value for the 'analyzeduration' (1000000) and 'probesize' (1000000) options")
	if !contOK || !contWarn || cont == "" {
		t.Fatalf("warning continuation = (%q, %v, %v), want surfaced warning", cont, contWarn, contOK)
	}

	// Info continuation stays quiet when indented, but top-level untagged
	// errors from drivers/external libraries must still surface.
	if _, _, ok := c.classify("[info] Input #0, flv, from 'pipe:0':"); ok {
		t.Fatal("info line should be suppressed")
	}
	if _, _, ok := c.classify("  Metadata:"); ok {
		t.Fatal("indented info continuation line should stay suppressed")
	}
	untaggedMsg, untaggedWarn, untaggedOK := c.classify("intel_enc_hw_context_init: Assertion 'encoder_context->mfc_context' failed")
	if !untaggedOK || untaggedWarn || untaggedMsg == "" {
		t.Fatalf("top-level untagged error after info = (%q, %v, %v), want surfaced error", untaggedMsg, untaggedWarn, untaggedOK)
	}

	// Empty input EOF detector matches 0-frame disconnect errors, not codec errors.
	if !isEmptyInputEOFLine("[vf#0:2 @ 0x1] [error] Cannot determine format of input 0:1 after EOF") {
		t.Fatal("expected empty input EOF detection")
	}
	if !isEmptyInputEOFLine("[vost#0:2/h264_nvenc @ 0x1] [error] Could not open encoder before EOF") {
		t.Fatal("expected encoder-before-EOF detection")
	}
	if isEmptyInputEOFLine("[error] Unknown encoder 'h264_nvenc'") {
		t.Fatal("real codec error must not be classified as empty input EOF")
	}
}

func TestTranscoderLogClassifierSuppressesPreLockOnH264MidGOPNoiseOnly(t *testing.T) {
	var c transcoderLogClassifier

	// Before "Stream mapping:", mid-GOP H.264 probe errors waiting for the first
	// SPS/PPS keyframe must be suppressed from the admin error log.
	for _, line := range []string{
		"[h264 @ 0x55f680e1e0c0] [error] sps_id 0 out of range",
		"[h264 @ 0x55f680e1e0c0] [error] non-existing PPS 0 referenced",
		"[h264 @ 0x55f680e1e0c0] [error] no frame!",
		"[h264 @ 0x55f680e1e0c0] [error] decode_slice_header error",
	} {
		if msg, _, ok := c.classify(line); ok {
			t.Fatalf("pre-lock-on mid-GOP line %q should be suppressed, surfaced %q", line, msg)
		}
	}

	// Real startup errors before Stream mapping must still surface.
	if msg, _, ok := c.classify("[error] Unknown encoder 'h264_nvenc'"); !ok || msg == "" {
		t.Fatal("real startup encoder error must still surface before Stream mapping")
	}

	// Once Stream mapping is logged, later H.264 errors indicate live mid-stream
	// corruption and must surface normally.
	if _, _, ok := c.classify("[info] Stream mapping:"); ok {
		t.Fatal("Stream mapping info line itself must stay quiet")
	}
	if msg, isWarn, ok := c.classify("[h264 @ 0x55f680e1e0c0] [error] non-existing PPS 0 referenced"); !ok || isWarn || msg == "" {
		t.Fatalf("post-lock-on H.264 error should surface, got (%q, %v, %v)", msg, isWarn, ok)
	}
}
