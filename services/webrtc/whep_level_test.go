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
}
