package webrtc

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

// The buffer delay is accepted only inside the range the old heartbeat used; a legitimate
// zero stays, everything unusable becomes unknown.
func TestSanitizedClientReportValidatesTheBufferDelay(t *testing.T) {
	zero := 0.0
	negative := -1.0
	atLimit := float64(maxClientLatencyMillisec)
	justUnder := float64(maxClientLatencyMillisec - 1)
	nan := math.NaN()
	infinite := math.Inf(1)

	cases := []struct {
		name    string
		input   *float64
		want    *float64
		wantNil bool
	}{
		{name: "missing", input: nil, wantNil: true},
		{name: "zero is legitimate", input: &zero, want: &zero},
		{name: "negative", input: &negative, wantNil: true},
		{name: "at the limit", input: &atLimit, wantNil: true},
		{name: "just under the limit", input: &justUnder, want: &justUnder},
		{name: "not a number", input: &nan, wantNil: true},
		{name: "infinite", input: &infinite, wantNil: true},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			report := sanitizedClientReport(ClientReport{LatencyMs: testCase.input})
			if testCase.wantNil {
				require.Nil(t, report.LatencyMs)

				return
			}
			require.NotNil(t, report.LatencyMs)
			require.Equal(t, *testCase.want, *report.LatencyMs)
		})
	}
}

// An unusable delay must not take the rest of the report with it.
func TestInvalidBufferDelayKeepsTheOtherMeasurements(t *testing.T) {
	bad := -5.0
	report := sanitizedClientReport(ClientReport{
		LatencyMs:     &bad,
		DecodedWidth:  1920,
		DecodedHeight: 1080,
		FPS:           30,
		JitterMs:      12,
		NameToken:     "token-a",
	})

	require.Nil(t, report.LatencyMs)
	require.Equal(t, 1920, report.DecodedWidth)
	require.Equal(t, 1080, report.DecodedHeight)
	require.Equal(t, 30.0, report.FPS)
	require.Equal(t, 12.0, report.JitterMs)
	require.Equal(t, "token-a", report.NameToken)
}
