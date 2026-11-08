package webrtc

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/owncast/owncast/models"
	"github.com/pion/rtp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingWriter struct {
	mu      sync.Mutex
	packets []rtp.Packet
}

func (w *recordingWriter) WriteRTP(packet *rtp.Packet) error {
	clone := *packet
	clone.Payload = append([]byte(nil), packet.Payload...)
	w.mu.Lock()
	w.packets = append(w.packets, clone)
	w.mu.Unlock()

	return nil
}

func (w *recordingWriter) snapshot() []rtp.Packet {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]rtp.Packet, len(w.packets))
	copy(out, w.packets)

	return out
}

func (w *recordingWriter) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()

	return len(w.packets)
}

type forwarderHarness struct {
	forwarder *forwarder
	writer    *recordingWriter
	conn      *net.UDPConn
	client    *net.UDPConn
}

func newForwarderHarness(t *testing.T, kind string, clockRate, delta uint32, payloadType uint8, reorderWindow time.Duration) *forwarderHarness {
	t.Helper()

	conn, err := listenLoopbackUDP()
	require.NoError(t, err)
	client, err := net.DialUDP("udp4", nil, conn.LocalAddr().(*net.UDPAddr))
	require.NoError(t, err)

	writer := &recordingWriter{}
	fwd := newForwarder(kind, conn, writer, clockRate, delta, payloadType, reorderWindow)

	var wg sync.WaitGroup
	fwd.start(&wg)
	t.Cleanup(func() {
		_ = conn.Close()
		_ = client.Close()
		wg.Wait()
	})

	return &forwarderHarness{forwarder: fwd, writer: writer, conn: conn, client: client}
}

func (h *forwarderHarness) send(t *testing.T, packet *rtp.Packet) {
	t.Helper()
	raw, err := packet.Marshal()
	require.NoError(t, err)
	_, err = h.client.Write(raw)
	require.NoError(t, err)
}

func (h *forwarderHarness) waitForPackets(t *testing.T, count int) []rtp.Packet {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if packets := h.writer.snapshot(); len(packets) >= count {
			return packets
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d packets, got %d", count, h.writer.count())

	return nil
}

func h264TestPacket(seq uint16, ts uint32, payload []byte) *rtp.Packet {
	return &rtp.Packet{
		Header:  rtp.Header{Version: 2, SequenceNumber: seq, Timestamp: ts, SSRC: 0x01020304, PayloadType: PayloadTypeVideo},
		Payload: payload,
	}
}

func TestForwarderDeliversAndNormalizes(t *testing.T) {
	harness := newForwarderHarness(t, "video", rtpClockRate, 3000, PayloadTypeVideo, 0)

	harness.send(t, h264TestPacket(100, 9000, []byte{0x65, 0x88}))
	harness.send(t, h264TestPacket(101, 12000, []byte{0x65, 0x88}))

	packets := harness.waitForPackets(t, 2)
	assert.EqualValues(t, 100, packets[0].SequenceNumber)
	assert.EqualValues(t, 101, packets[1].SequenceNumber)
	assert.EqualValues(t, 9000, packets[0].Timestamp)
	assert.EqualValues(t, 12000, packets[1].Timestamp)
}

func TestForwarderRebasesAcrossProducerRestart(t *testing.T) {
	harness := newForwarderHarness(t, "video", rtpClockRate, 3000, PayloadTypeVideo, 0)

	harness.send(t, h264TestPacket(100, 9000, []byte{0x65}))
	harness.send(t, h264TestPacket(101, 12000, []byte{0x65}))
	harness.waitForPackets(t, 2)

	// A new FFmpeg run restarts its RTP counters.
	harness.send(t, h264TestPacket(40000, 500, []byte{0x65}))
	packets := harness.waitForPackets(t, 3)
	assert.EqualValues(t, 102, packets[2].SequenceNumber)
	assert.EqualValues(t, 15000, packets[2].Timestamp)
}

func TestForwarderCachesParameterSetsAndKeyframes(t *testing.T) {
	harness := newForwarderHarness(t, "video", rtpClockRate, 3000, PayloadTypeVideo, 0)

	sps := []byte{0x67, 0x42, 0xc0, 0x1f}
	pps := []byte{0x68, 0xce, 0x3c, 0x80}
	stapA := []byte{nalTypeSTAPA, 0, byte(len(sps))}
	stapA = append(stapA, sps...)
	stapA = append(stapA, 0, byte(len(pps)))
	stapA = append(stapA, pps...)

	harness.send(t, h264TestPacket(1, 0, stapA))
	harness.send(t, h264TestPacket(2, 3000, []byte{nalTypeIDR, 0x88}))
	harness.waitForPackets(t, 2)

	deadline := time.Now().Add(2 * time.Second)
	var gotSPS, gotPPS []byte
	for time.Now().Before(deadline) {
		gotSPS, gotPPS = harness.forwarder.parameterSets()
		if len(gotSPS) > 0 && len(gotPPS) > 0 && !harness.forwarder.lastIDRAt().IsZero() {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	assert.Equal(t, sps, gotSPS)
	assert.Equal(t, pps, gotPPS)
	assert.False(t, harness.forwarder.lastIDRAt().IsZero(), "IDR timestamp should be recorded")
}

func TestForwarderCountsParseErrorsAndPayloadTypeMismatch(t *testing.T) {
	harness := newForwarderHarness(t, "video", rtpClockRate, 3000, PayloadTypeVideo, 0)

	_, err := harness.client.Write([]byte{0x80, 0x01})
	require.NoError(t, err)

	mismatch := h264TestPacket(1, 0, []byte{0x65})
	mismatch.PayloadType = 100
	harness.send(t, mismatch)
	harness.waitForPackets(t, 1)

	packets, _, parseErrors, ptMismatch, _, _, _, _, _ := harness.forwarder.stats()
	assert.EqualValues(t, 1, packets)
	assert.EqualValues(t, 1, parseErrors)
	assert.EqualValues(t, 1, ptMismatch)
}

func TestForwarderReorderWindow(t *testing.T) {
	harness := newForwarderHarness(t, "video", rtpClockRate, 3000, PayloadTypeVideo, 50*time.Millisecond)

	harness.send(t, h264TestPacket(10, 0, []byte{0x65}))
	harness.send(t, h264TestPacket(12, 6000, []byte{0x65}))
	harness.send(t, h264TestPacket(11, 3000, []byte{0x65}))

	packets := harness.waitForPackets(t, 3)
	sequences := []uint16{packets[0].SequenceNumber, packets[1].SequenceNumber, packets[2].SequenceNumber}
	assert.Equal(t, []uint16{10, 11, 12}, sequences)
}

func TestMediaLegLifecycleAndStats(t *testing.T) {
	svc := testService(t, nil)
	require.NoError(t, svc.SetVariants(testWebRTCVariants(1)))

	leg, err := svc.OpenMediaLeg(0)
	require.NoError(t, err)
	assert.EqualValues(t, 96, PayloadTypeVideo)
	assert.EqualValues(t, 111, PayloadTypeAudio)
	assert.Contains(t, leg.VideoURL(), "rtp://127.0.0.1:")
	assert.Contains(t, leg.VideoURL(), "?pkt_size=1200")
	assert.Contains(t, leg.AudioURL(), "?pkt_size=1200")
	assert.Greater(t, leg.VideoPort(), 0)
	assert.Greater(t, leg.AudioPort(), 0)
	assert.NotEqual(t, leg.VideoPort(), leg.AudioPort())

	stapA := []byte{nalTypeSTAPA, 0, 4, nalTypeSPS, 0x42, 0xc0, 0x1f, 0, 2, nalTypePPS, 0xce}
	sendToLeg(t, leg.VideoPort(), h264TestPacket(1, 0, stapA))
	sendToLeg(t, leg.VideoPort(), h264TestPacket(2, 3000, []byte{nalTypeIDR, 0x88}))
	audio := &rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: 1, Timestamp: 960, SSRC: 0x55, PayloadType: PayloadTypeAudio}, Payload: []byte{0x01, 0x02}}
	sendToLeg(t, leg.AudioPort(), audio)

	deadline := time.Now().Add(3 * time.Second)
	var stats MediaLegStats
	for time.Now().Before(deadline) {
		stats = leg.Stats()
		if stats.VideoPackets >= 2 && stats.AudioPackets >= 1 && stats.SPSSeen && stats.PPSSeen && stats.LastKeyframeAgeMs >= 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	assert.GreaterOrEqual(t, stats.VideoPackets, uint64(2))
	assert.GreaterOrEqual(t, stats.AudioPackets, uint64(1))
	assert.True(t, stats.SPSSeen)
	assert.True(t, stats.PPSSeen)
	assert.GreaterOrEqual(t, stats.LastKeyframeAgeMs, int64(0))

	require.NoError(t, svc.CloseMediaLeg(0))
	require.NoError(t, svc.CloseMediaLeg(0))

	_, err = svc.OpenMediaLeg(3)
	require.ErrorIs(t, err, ErrVariantNotFound)
}

func TestMediaLegPacketSizeConfiguration(t *testing.T) {
	svc := testService(t, func(cfg *models.WebRTCConfig) { cfg.RTPPacketSize = 800 })
	require.NoError(t, svc.SetVariants(testWebRTCVariants(1)))

	leg, err := svc.OpenMediaLeg(0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.CloseMediaLeg(0) })
	assert.Contains(t, leg.VideoURL(), "?pkt_size=800")
}

func sendToLeg(t *testing.T, port int, packet *rtp.Packet) {
	t.Helper()
	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	require.NoError(t, err)
	defer conn.Close() //nolint:errcheck
	raw, err := packet.Marshal()
	require.NoError(t, err)
	_, err = conn.Write(raw)
	require.NoError(t, err)
}
