package livestatus

import (
	"testing"

	"github.com/owncast/owncast/models"
)

func TestApplyWebRTCLegsToOutboundReconcilesDisabledAndTranscodeFallback(t *testing.T) {
	broadcast := &models.CurrentBroadcast{
		OutputSettings: []models.StreamOutputVariant{
			{
				Name:               "Source HLS+WebRTC",
				IsVideoPassthrough: true,
				IsAudioPassthrough: true,
				Outputs: &models.VariantOutputs{
					HLS:    &models.ProtocolOutputSettings{Enabled: boolValue(true)},
					WebRTC: &models.ProtocolOutputSettings{Enabled: boolValue(true)},
				},
			},
			{
				Name:               "WebRTC Only Copy Fallback",
				IsVideoPassthrough: true,
				IsAudioPassthrough: true,
				Outputs: &models.VariantOutputs{
					HLS:    &models.ProtocolOutputSettings{Enabled: boolValue(false)},
					WebRTC: &models.ProtocolOutputSettings{Enabled: boolValue(true)},
				},
			},
			{
				Name:               "Passthrough Unknown Bitrate",
				IsVideoPassthrough: true,
				IsAudioPassthrough: true,
				Outputs: &models.VariantOutputs{
					HLS:    &models.ProtocolOutputSettings{Enabled: boolValue(true)},
					WebRTC: &models.ProtocolOutputSettings{Enabled: boolValue(true)},
				},
			},
		},
	}

	outbound := outboundStatus(broadcast, nil, "h264_nvenc", true, nil)
	measured := 4321.5
	legs := []models.WebRTCLegStatus{
		{
			VariantIndex: 0,
			Mode:         models.WebRTCLegModeDisabled,
		},
		{
			VariantIndex:     1,
			Mode:             models.WebRTCLegModeTranscode,
			VideoBitrateKbps: 3500,
		},
		{
			VariantIndex:      2,
			Mode:              models.WebRTCLegModeCopy,
			OutputBitrateKbps: &measured,
		},
	}

	applyWebRTCLegsToOutbound(&outbound, legs, "h264_nvenc")

	// Variant 0 had WebRTC disabled/withdrawn -> only HLS remains.
	if len(outbound.Streams[0].Protocols) != 1 || outbound.Streams[0].Protocols[0] != "hls" {
		t.Fatalf("variant 0 protocols = %v, want [hls]", outbound.Streams[0].Protocols)
	}

	// Variant 1 is WebRTC-only and fell back from copy to transcode -> encoder and Opus audio updated.
	v1 := outbound.Streams[1]
	if v1.VideoEncoder != "h264_nvenc" {
		t.Fatalf("variant 1 video encoder = %q, want h264_nvenc", v1.VideoEncoder)
	}
	if v1.AudioEncoder != "libopus" {
		t.Fatalf("variant 1 audio encoder = %q, want libopus", v1.AudioEncoder)
	}
	if v1.VideoBitrateKbps == nil || *v1.VideoBitrateKbps != 3500 {
		t.Fatalf("variant 1 video bitrate = %v, want 3500", v1.VideoBitrateKbps)
	}

	// Variant 2 had nil source bitrate -> reconciled with measured leg bitrate.
	v2 := outbound.Streams[2]
	if v2.VideoBitrateKbps == nil || *v2.VideoBitrateKbps != measured {
		t.Fatalf("variant 2 video bitrate = %v, want %v", v2.VideoBitrateKbps, measured)
	}
}
