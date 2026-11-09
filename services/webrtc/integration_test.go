package webrtc

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEndToEndMediaDelivery is the Phase 3 proof that a WHEP session receives
// media fed through a Go-owned loopback leg: Pion viewer <-> WHEP session <->
// MediaLeg RTP sockets. It mirrors the Phase 0 probe's FFmpeg leg check without
// requiring FFmpeg.
func TestEndToEndMediaDelivery(t *testing.T) {
	svc := testService(t, nil)
	require.NoError(t, svc.SetVariants(testWebRTCVariants(1)))

	clientPC, offer := testBrowserOfferWithCandidates(t)

	var (
		mu    sync.Mutex
		video []rtp.Packet
		audio []rtp.Packet
	)
	clientPC.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		go func() {
			for {
				packet, _, err := track.ReadRTP()
				if err != nil {
					return
				}
				clone := *packet
				clone.Payload = append([]byte(nil), packet.Payload...)
				mu.Lock()
				if track.Kind() == webrtc.RTPCodecTypeVideo {
					video = append(video, clone)
				} else {
					audio = append(audio, clone)
				}
				mu.Unlock()
			}
		}()
	})

	created := whepRequest(t, svc, http.MethodPost, "/webrtc/whep", "application/sdp", offer, nil)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	answer := created.Body.String()

	require.NoError(t, clientPC.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer}))
	require.Eventually(t, func() bool {
		return clientPC.ConnectionState() == webrtc.PeerConnectionStateConnected
	}, 10*time.Second, 20*time.Millisecond, "viewer PeerConnection did not connect")

	leg, err := svc.OpenMediaLeg(0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.CloseMediaLeg(0) })

	// Feed a few frames through the loopback sockets, exactly as FFmpeg would.
	stapA := []byte{nalTypeSTAPA, 0, 4, nalTypeSPS, 0x42, 0xc0, 0x1f, 0, 2, nalTypePPS, 0xce}
	sendToLeg(t, leg.VideoPort(), &rtp.Packet{
		Header:  rtp.Header{Version: 2, SequenceNumber: 1, Timestamp: 0, SSRC: 0x1111, PayloadType: PayloadTypeVideo, Marker: true},
		Payload: stapA,
	})
	for i := 0; i < 5; i++ {
		sendToLeg(t, leg.VideoPort(), &rtp.Packet{
			Header: rtp.Header{
				Version: 2, SequenceNumber: uint16(2 + i), Timestamp: uint32((i + 1) * 3000),
				SSRC: 0x1111, PayloadType: PayloadTypeVideo, Marker: true,
			},
			Payload: []byte{nalTypeIDR, 0x88, 0x84},
		})
		sendToLeg(t, leg.AudioPort(), &rtp.Packet{
			Header: rtp.Header{
				Version: 2, SequenceNumber: uint16(1 + i), Timestamp: uint32(960 * (i + 1)),
				SSRC: 0x2222, PayloadType: PayloadTypeAudio,
			},
			Payload: []byte{0xf8, 0xff, 0xfe},
		})
	}

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()

		return len(video) >= 3 && len(audio) >= 3
	}, 5*time.Second, 20*time.Millisecond, "viewer did not receive forwarded RTP")

	mu.Lock()
	videoCount := len(video)
	audioCount := len(audio)
	firstVideoSSRC := video[0].Header.SSRC
	firstAudioSSRC := audio[0].Header.SSRC
	mu.Unlock()

	assert.GreaterOrEqual(t, videoCount, 3)
	assert.GreaterOrEqual(t, audioCount, 3)
	// Pion must have rewritten the FFmpeg-side SSRC per binding.
	assert.NotEqualValues(t, uint32(0x1111), firstVideoSSRC)
	assert.NotEqualValues(t, uint32(0x2222), firstAudioSSRC)

	// The admin row must expose which ICE transport the connection selected.
	// Detection reads the nominated+succeeded candidate pair from
	// PeerConnection.GetStats(), which the janitor samples every 2 seconds.
	require.Eventually(t, func() bool {
		viewers := svc.LiveViewers()
		return len(viewers) == 1 && viewers[0].ICETransport == "udp"
	}, 8*time.Second, 50*time.Millisecond, "server did not detect the selected ICE transport (udp)")
}
