package transcoder

import (
	"os"
	"strings"
	"testing"

	"github.com/owncast/owncast/config"
	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/ingest"
)

const goldenHLSArgvFixture = "testdata/golden-default-hls-argv.txt"

// newGoldenTranscoder builds a transcoder with the settings of the v0.3.1
// default configuration that produced the captured golden argv. The values
// (latency level 2, 1200kbps/24fps/veryfast variant, audio copy) mirror
// artifacts/phase0/baseline-argv/default.config.serverconfig.json.
func newGoldenTranscoder() *Transcoder {
	tr := new(Transcoder)
	tr.cfg = &config.Config{LogDirectory: "data/logs"}
	tr.ffmpegPath = "ffmpeg"
	tr.internalListenerPort = "8927"
	tr.input = "pipe:0"
	tr.inputFormat = ingest.FLVInputFormat()
	tr.currentLatencyLevel = models.GetLatencyLevel(2)
	tr.codec = &Libx264Codec{}
	tr.SetIdentifier("goldenid")

	variant := HLSVariant{
		videoBitrate:       1200,
		isAudioPassthrough: true,
		cpuUsageLevel:      2,
	}
	variant.SetVideoFramerate(24)
	tr.AddVariant(variant)

	return tr
}

// TestGoldenDefaultHLSArgv is the backward-compatibility gate from the master
// spec (Section 11): the default HLS-only configuration must produce exactly
// the argv that v0.3.1 produced. The fixture was captured from
// /proc/<ffmpeg-child>/cmdline of a live unmodified binary, then normalized
// only for the ffmpeg path, the ephemeral internal HLS port and the shortid
// segment identifier.
func TestGoldenDefaultHLSArgv(t *testing.T) {
	wantBytes, err := os.ReadFile(goldenHLSArgvFixture)
	if err != nil {
		t.Fatalf("reading golden fixture: %v", err)
	}
	want := strings.Split(strings.TrimSpace(string(wantBytes)), "\n")

	tr := newGoldenTranscoder()
	got := tr.getFlags().command

	if len(got) != len(want) {
		t.Fatalf("argv length changed: got %d args, want %d\ngot:  %q\nwant: %q", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("argv[%d] changed: got %q, want %q\nfull got:  %q\nfull want: %q", i, got[i], want[i], got, want)
		}
	}
}

// TestTranscoderInputFormatMPEGTS proves the SRT input descriptor adds the
// explicit container flag immediately before -i and that it does not disturb
// the rest of the argv.
func TestTranscoderInputFormatMPEGTS(t *testing.T) {
	tr := newGoldenTranscoder()
	tr.SetInputFormat(ingest.MPEGTSInputFormat())

	got := tr.getFlags().command

	// Locate -i and assert "-f mpegts" directly precedes it.
	inputIndex := -1
	for i, arg := range got {
		if arg == "-i" {
			inputIndex = i
			break
		}
	}
	if inputIndex < 2 {
		t.Fatalf("could not find -i in argv: %q", got)
	}
	if got[inputIndex-2] != "-f" || got[inputIndex-1] != "mpegts" {
		t.Fatalf("mpegts input flag not before -i: %q", got[inputIndex-3:inputIndex+1])
	}

	// The rest of the argv is identical to the FLV argv with the descriptor
	// removed.
	flv := newGoldenTranscoder().getFlags().command
	var stripped []string
	for i := 0; i < len(got); i++ {
		if got[i] == "-f" && i+1 < len(got) && got[i+1] == "mpegts" && i+2 < len(got) && got[i+2] == "-i" {
			i++ // skip -f mpegts
			continue
		}
		stripped = append(stripped, got[i])
	}
	if len(stripped) != len(flv) {
		t.Fatalf("unexpected argv delta: got %d, want %d", len(stripped), len(flv))
	}
	for i := range flv {
		if stripped[i] != flv[i] {
			t.Fatalf("arg %d differs between FLV and MPEG-TS argv: %q vs %q", i, stripped[i], flv[i])
		}
	}
}
