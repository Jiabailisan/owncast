package transcoder

import (
	"slices"
	"testing"
)

// sc_threshold is a libx264 AVOption. Emitting it for a hardware encoder produces a
// "has not been used for any stream" warning on every start, once per output, and has no effect.
func TestScThresholdFlagsOnlyForLibx264(t *testing.T) {
	cases := []struct {
		codec Codec
		want  bool
	}{
		{&Libx264Codec{}, true},
		{&NvencCodec{}, false},
		{&VaapiCodec{}, false},
		{&QuicksyncCodec{}, false},
		{&VideoToolboxCodec{}, false},
		{&OmxCodec{}, false},
	}

	for _, tc := range cases {
		t.Run(tc.codec.Name(), func(t *testing.T) {
			got := scThresholdFlags(tc.codec)
			if tc.want {
				if !slices.Equal(got, []string{"-sc_threshold", "0"}) {
					t.Fatalf("%s should keep the scene change flag, got %v", tc.codec.Name(), got)
				}

				return
			}
			if len(got) != 0 {
				t.Fatalf("%s does not define sc_threshold, got %v", tc.codec.Name(), got)
			}
		})
	}
}

func TestScThresholdFlagsNilCodec(t *testing.T) {
	if got := scThresholdFlags(nil); len(got) != 0 {
		t.Fatalf("nil codec must produce no flags, got %v", got)
	}
}
