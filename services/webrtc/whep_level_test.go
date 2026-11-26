package webrtc

import (
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
