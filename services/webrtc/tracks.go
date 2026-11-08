package webrtc

import (
	"fmt"

	"github.com/pion/webrtc/v4"
)

// Variant is the WebRTC media description of one delivery variant. It mirrors
// the information the public config and the admin viewer table need; the
// transcoder plan builder is the source of the actual encoding parameters.
type Variant struct {
	Index  int    `json:"index"`
	Name   string `json:"name"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
	FPS    int    `json:"fps,omitempty"`
}

// variantTracks is the pair of shared Pion tracks for one variant. Every viewer
// of the variant writes into these tracks; Pion rewrites the payload type and
// SSRC per viewer binding, so the forwarder writes packets unchanged.
type variantTracks struct {
	variant  Variant
	h264Fmtp string
	video    *webrtc.TrackLocalStaticRTP
	audio    *webrtc.TrackLocalStaticRTP
}

func newVariantTracks(variant Variant, h264Fmtp string) (*variantTracks, error) {
	streamID := fmt.Sprintf("owncast-variant-%d", variant.Index)

	video, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{
		MimeType:    webrtc.MimeTypeH264,
		ClockRate:   rtpClockRate,
		SDPFmtpLine: h264Fmtp,
	}, "video", streamID)
	if err != nil {
		return nil, fmt.Errorf("create video track: %w", err)
	}

	audio, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{
		MimeType:    webrtc.MimeTypeOpus,
		ClockRate:   opusClockRate,
		Channels:    opusChannels,
		SDPFmtpLine: canonicalOpusFmtp,
	}, "audio", streamID)
	if err != nil {
		return nil, fmt.Errorf("create audio track: %w", err)
	}

	return &variantTracks{variant: variant, h264Fmtp: h264Fmtp, video: video, audio: audio}, nil
}

// compatible reports whether an existing track set can be reused for a variant
// with the same delivery parameters. Reusing it keeps existing viewers
// connected across plan rebuilds for an unchanged stream.
func (vt *variantTracks) compatible(variant Variant, h264Fmtp string) bool {
	return vt.variant == variant && vt.h264Fmtp == h264Fmtp
}

func (vt *variantTracks) resolution() string {
	if vt.variant.Width > 0 && vt.variant.Height > 0 {
		return fmt.Sprintf("%dx%d", vt.variant.Width, vt.variant.Height)
	}

	return ""
}
