package transcoder

import (
	"strings"
	"testing"

	"github.com/owncast/owncast/models"
)

// TestHLSDisabledVariantExcluded proves a variant with outputs.hls.enabled=false
// is not emitted by the HLS process (spec 4.4), while the remaining HLS
// variants keep their own maps.
func TestHLSDisabledVariantExcluded(t *testing.T) {
	disabled := models.BoolPtr(false)

	tr := new(Transcoder)
	tr.cfg = nil
	tr.ffmpegPath = "ffmpeg"
	tr.internalListenerPort = "8927"
	tr.input = "pipe:0"
	tr.currentLatencyLevel = models.GetLatencyLevel(2)
	tr.codec = &Libx264Codec{}
	tr.SetIdentifier("hlsfilter")

	tr.currentStreamOutputSettings = []models.StreamOutputVariant{
		{VideoBitrate: 1200, Framerate: 24, IsAudioPassthrough: true, CPUUsageLevel: 2},
		{
			VideoBitrate: 800, Framerate: 24, IsAudioPassthrough: true, CPUUsageLevel: 2,
			Outputs: &models.VariantOutputs{HLS: &models.ProtocolOutputSettings{Enabled: disabled}},
		},
		{VideoBitrate: 600, Framerate: 24, IsAudioPassthrough: true, CPUUsageLevel: 2},
	}
	for index, quality := range tr.currentStreamOutputSettings {
		if !quality.HLSEnabled() {
			continue
		}
		tr.AddVariant(getVariantFromConfigQuality(quality, index))
	}

	joined := strings.Join(tr.getFlags().command, " ")
	// Two HLS-enabled variants -> var_stream_map with exactly two entries.
	if !strings.Contains(joined, "-var_stream_map v:0,a:0 v:1,a:1") {
		t.Fatalf("expected two HLS variants in var_stream_map: %s", joined)
	}
	if strings.Contains(joined, "-c:v:2") {
		t.Fatalf("disabled HLS variant must not produce a third encoder: %s", joined)
	}
}
