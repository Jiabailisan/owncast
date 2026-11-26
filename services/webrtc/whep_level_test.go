package webrtc

import (
	"net/http"
	"strings"
	"testing"

	"github.com/owncast/owncast/models"
	"github.com/pion/webrtc/v4"
)

func TestRewriteAnswerH264Level(t *testing.T) {
	answer := &webrtc.SessionDescription{SDP: "v=0\r\na=fmtp:116 level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=4d001f\r\n"}
	rewriteAnswerH264Level(answer, models.WebRTCConfig{H264Profile: "main", H264Level: "auto"})
	if !strings.Contains(answer.SDP, "profile-level-id=4d0033") {
		t.Fatalf("auto main answer did not advertise level 5.1: %s", answer.SDP)
	}
	answer.SDP = "a=fmtp:116 level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=4d001f"
	rewriteAnswerH264Level(answer, models.WebRTCConfig{H264Profile: "main", H264Level: "4.0"})
	if !strings.Contains(answer.SDP, "profile-level-id=4d0028") {
		t.Fatalf("configured level 4.0 not honored in answer: %s", answer.SDP)
	}
	// Strict clients such as Firefox require the offered profile bytes to be
	// preserved; only the level byte may change.
	answer.SDP = "a=fmtp:116 level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f"
	rewriteAnswerH264Level(answer, models.WebRTCConfig{H264Profile: "main", H264Level: "auto"})
	if !strings.Contains(answer.SDP, "profile-level-id=42e033") {
		t.Fatalf("offered profile must be preserved for Firefox: %s", answer.SDP)
	}
}

func TestRewriteAnswerH264LevelForCopyLegPreservesHigherSourceLevel(t *testing.T) {
	cfg := models.WebRTCConfig{H264Profile: "main", H264Level: "4.2"}

	// Copy leg carrying a 1440p60 Level 5.1 source raises the answer SDP level
	// to 5.1 (0x33) even though transcoded legs are configured at Level 4.2 (0x2a).
	copyLeg := &MediaLeg{}
	copyLeg.SetCopySourceLevel("5.1")
	copyAnswer := &webrtc.SessionDescription{SDP: "a=fmtp:116 level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=4d001f"}
	rewriteAnswerH264LevelForLeg(copyAnswer, cfg, copyLeg)
	if !strings.Contains(copyAnswer.SDP, "profile-level-id=4d0033") {
		t.Fatalf("copy leg answer did not raise level to source 5.1: %s", copyAnswer.SDP)
	}

	// Transcode leg (no copy source level) keeps the configured Level 4.2 (0x2a).
	transcodeLeg := &MediaLeg{}
	transcodeAnswer := &webrtc.SessionDescription{SDP: "a=fmtp:116 level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=4d001f"}
	rewriteAnswerH264LevelForLeg(transcodeAnswer, cfg, transcodeLeg)
	if !strings.Contains(transcodeAnswer.SDP, "profile-level-id=4d002a") {
		t.Fatalf("transcode leg answer did not honor configured 4.2: %s", transcodeAnswer.SDP)
	}
}

func TestWHEPNegotiatesSourceHighProfileForCopyLegAndMainForTranscodeLeg(t *testing.T) {
	svc := testService(t, func(cfg *models.WebRTCConfig) {
		cfg.H264Profile = "main"
		cfg.H264Level = "4.2"
	})
	if err := setVariants(svc, testWebRTCVariants(2)); err != nil {
		t.Fatalf("setVariants: %v", err)
	}
	copyLeg, err := svc.OpenMediaLeg(0, svc.VariantGeneration())
	if err != nil {
		t.Fatalf("open copy leg: %v", err)
	}
	t.Cleanup(func() { _ = svc.CloseMediaLeg(0) })
	copyLeg.SetCopySourceFormat("constrained-high", "5.1")

	_, err = svc.OpenMediaLeg(1, svc.VariantGeneration())
	if err != nil {
		t.Fatalf("open transcode leg: %v", err)
	}
	t.Cleanup(func() { _ = svc.CloseMediaLeg(1) })

	// Browser offer advertising Baseline (42e01f), Main (4d001f) and High (64001f).
	me := &webrtc.MediaEngine{}
	for idx, fmtpLine := range []string{
		"level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f",
		"level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=4d001f",
		"level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=64001f",
	} {
		if err := me.RegisterCodec(webrtc.RTPCodecParameters{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:    webrtc.MimeTypeH264,
				ClockRate:   90000,
				SDPFmtpLine: fmtpLine,
			},
			PayloadType: webrtc.PayloadType(102 + idx*2),
		}, webrtc.RTPCodecTypeVideo); err != nil {
			t.Fatalf("register video codec: %v", err)
		}
	}
	if err := me.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:    webrtc.MimeTypeOpus,
			ClockRate:   48000,
			Channels:    2,
			SDPFmtpLine: canonicalOpusFmtp,
		},
		PayloadType: 111,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		t.Fatalf("register audio codec: %v", err)
	}

	makeOffer := func() (*webrtc.PeerConnection, string) {
		api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
		pc, err := api.NewPeerConnection(webrtc.Configuration{})
		if err != nil {
			t.Fatalf("new pc: %v", err)
		}
		t.Cleanup(func() { _ = pc.Close() })
		for _, kind := range []webrtc.RTPCodecType{webrtc.RTPCodecTypeVideo, webrtc.RTPCodecTypeAudio} {
			if _, err := pc.AddTransceiverFromKind(kind, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
				t.Fatalf("add transceiver: %v", err)
			}
		}
		offer, err := pc.CreateOffer(nil)
		if err != nil {
			t.Fatalf("create offer: %v", err)
		}
		if err := pc.SetLocalDescription(offer); err != nil {
			t.Fatalf("set local desc: %v", err)
		}
		return pc, pc.LocalDescription().SDP
	}

	pc0, offer0 := makeOffer()
	resp0 := whepRequest(t, svc, http.MethodPost, "/webrtc/whep?variant=0", "application/sdp", offer0, nil)
	if resp0.Code != http.StatusCreated {
		t.Fatalf("variant 0 WHEP failed (%d): %s", resp0.Code, resp0.Body.String())
	}
	answer0 := resp0.Body.String()
	if !strings.Contains(answer0, "profile-level-id=640033") {
		t.Fatalf("copy leg (constrained-high 5.1) should negotiate High 5.1 (640033), got:\n%s", answer0)
	}
	if err := pc0.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer0}); err != nil {
		t.Fatalf("browser rejected variant 0 answer: %v", err)
	}

	pc1, offer1 := makeOffer()
	resp1 := whepRequest(t, svc, http.MethodPost, "/webrtc/whep?variant=1", "application/sdp", offer1, nil)
	if resp1.Code != http.StatusCreated {
		t.Fatalf("variant 1 WHEP failed (%d): %s", resp1.Code, resp1.Body.String())
	}
	answer1 := resp1.Body.String()
	if !strings.Contains(answer1, "profile-level-id=4d002a") {
		t.Fatalf("transcode leg (main 4.2) should negotiate Main 4.2 (4d002a), got:\n%s", answer1)
	}
	if err := pc1.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer1}); err != nil {
		t.Fatalf("browser rejected variant 1 answer: %v", err)
	}
}
