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
