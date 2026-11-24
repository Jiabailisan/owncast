//go:build integration

package webrtc_e2e

// End-to-end proof that the rendition a WHEP viewer receives is the rendition
// the output plan describes. The SDP answer only advertises a profile-level-id,
// so the dimensions have to come from the bitstream the viewer actually
// receives: this test depacketizes the H.264 RTP and reads the SPS.
//
// Run against a live server (the QA runner starts one and publishes to it):
//
//	OWNCAST_BASE_URL=http://127.0.0.1:19120 \
//	OWNCAST_WHEP_EXPECT_LIVE=1 \
//	OWNCAST_WHEP_EXPECT_RESOLUTION=2560x1440 \
//	go test -tags=integration -count=1 -run TestWHEPMediaResolution ./test/automated/webrtc/...

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"

	"github.com/owncast/owncast/services/ingest/mpegts"
)

// TestWHEPMediaResolution asserts the decoded resolution of the video a WHEP
// viewer receives.
func TestWHEPMediaResolution(t *testing.T) {
	if os.Getenv("OWNCAST_WHEP_EXPECT_LIVE") != "1" {
		t.Skip("set OWNCAST_WHEP_EXPECT_LIVE=1 with a live RTMP/SRT publisher for the media assertions")
	}
	h := newHarness(t)

	peerConnection, offer := browserOffer(t)
	defer peerConnection.Close()

	media := &receivedMedia{}
	media.attach(t, peerConnection)

	response := h.postOffer(offer, nil)
	body := readBody(t, response)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("POST status %d, want 201 (body %s)", response.StatusCode, body)
	}
	if err := peerConnection.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  body,
	}); err != nil {
		t.Fatalf("set remote answer: %v", err)
	}
	waitConnected(t, peerConnection)

	deadline := time.Now().Add(mediaTimeout)
	for time.Now().Before(deadline) {
		video, _, _, _ := media.snapshot()
		if sps := findReceivedSPS(video); sps != nil {
			parsed, ok := mpegts.ParseSPSNAL(sps)
			if !ok {
				// A partially received NAL can look like an SPS; keep
				// accumulating until a complete one parses.
				time.Sleep(50 * time.Millisecond)

				continue
			}
			size := fmt.Sprintf("%dx%d", parsed.Width, parsed.Height)
			t.Logf("received H.264 SPS: %s %s L%s", size, parsed.ProfileName(), parsed.Level())

			if expected := os.Getenv("OWNCAST_WHEP_EXPECT_RESOLUTION"); expected != "" && size != expected {
				t.Fatalf("decoded size is %s, want %s", size, expected)
			}

			return
		}
		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("no H.264 SPS was received within %s", mediaTimeout)
}

// findReceivedSPS depacketizes the received RTP and returns the first H.264 SPS
// NAL unit (without its start code), or nil.
func findReceivedSPS(packets []rtp.Packet) []byte {
	var depacketizer codecs.H264Packet
	stream := make([]byte, 0, 64*1024)
	for index := range packets {
		payload, err := depacketizer.Unmarshal(packets[index].Payload)
		if err != nil {
			continue
		}
		stream = append(stream, payload...)
		if sps := firstSPS(stream); sps != nil {
			return sps
		}
	}

	return nil
}

// firstSPS returns the first *complete* SPS NAL in an Annex-B elementary
// stream. A NAL is only complete once the next start code has arrived.
func firstSPS(stream []byte) []byte {
	start := -1
	for index := 0; index+3 <= len(stream); index++ {
		headerLen := 0
		if stream[index] == 0 && stream[index+1] == 0 {
			if stream[index+2] == 1 {
				headerLen = 3
			} else if index+4 <= len(stream) && stream[index+2] == 0 && stream[index+3] == 1 {
				headerLen = 4
			}
		}
		if headerLen == 0 {
			continue
		}
		if start >= 0 {
			if nal := trimNAL(stream[start:index]); nal != nil && nal[0]&0x1f == 7 {
				return nal
			}
		}
		start = index + headerLen
		index += headerLen - 1
	}
	// The trailing NAL is deliberately ignored: it is still being received, so
	// its bytes are not a complete NAL yet.
	return nil
}

// trimNAL drops the zero padding an Annex-B writer may leave before the next
// start code.
func trimNAL(nal []byte) []byte {
	nal = bytes.TrimRight(nal, "\x00")
	if len(nal) == 0 {
		return nil
	}

	return nal
}
