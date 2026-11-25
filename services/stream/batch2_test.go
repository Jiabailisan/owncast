package stream

import (
	"bytes"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/ingest"
	"github.com/owncast/owncast/services/webrtc"
)

type bitrateMeasuringSession struct {
	reader      io.Reader
	done        chan struct{}
	sourceReady chan struct{}

	mu          sync.Mutex
	broadcaster *models.Broadcaster
}

func (s *bitrateMeasuringSession) ID() string                       { return "srt-bitrate-test" }
func (s *bitrateMeasuringSession) Protocol() ingest.Protocol        { return ingest.ProtocolSRT }
func (s *bitrateMeasuringSession) StreamKeyID() string              { return "key:test" }
func (s *bitrateMeasuringSession) StreamTitle() string              { return "" }
func (s *bitrateMeasuringSession) RemoteAddr() net.Addr             { return nil }
func (s *bitrateMeasuringSession) Reader() io.Reader                { return s.reader }
func (s *bitrateMeasuringSession) InputFormat() ingest.InputFormat  { return ingest.MPEGTSInputFormat() }
func (s *bitrateMeasuringSession) Age() time.Duration               { return 0 }
func (s *bitrateMeasuringSession) Stats() ingest.Stats              { return ingest.Stats{} }
func (s *bitrateMeasuringSession) Done() <-chan struct{}            { return s.done }
func (s *bitrateMeasuringSession) Close(error)                      {}
func (s *bitrateMeasuringSession) SourceInfoReady() <-chan struct{} { return s.sourceReady }
func (s *bitrateMeasuringSession) SetBroadcaster(b *models.Broadcaster) {
	s.mu.Lock()
	s.broadcaster = b
	s.mu.Unlock()
}
func (s *bitrateMeasuringSession) Broadcaster() *models.Broadcaster {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.broadcaster
}

type inspectingChunkReader struct {
	data      []byte
	pos       int
	threshold int
	onReached func()
	triggered bool
}

func (r *inspectingChunkReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		time.Sleep(5 * time.Millisecond)

		return 0, io.EOF
	}
	n := 16 * 1024
	if n > len(p) {
		n = len(p)
	}
	if r.pos+n > len(r.data) {
		n = len(r.data) - r.pos
	}
	copy(p, r.data[r.pos:r.pos+n])
	r.pos += n
	if !r.triggered && r.pos >= r.threshold {
		r.triggered = true
		r.onReached()
	}

	return n, nil
}

// TestAwaitSourceBitratePumpsMediaReaderAndReplaysAllBytes proves that
// awaitSourceBitrate actively pumps mediaReader so the ingest inspector can
// publish a measured bitrate before FFmpeg starts, and replays every byte
// intact afterwards.
func TestAwaitSourceBitratePumpsMediaReaderAndReplaysAllBytes(t *testing.T) {
	payload := bytes.Repeat([]byte{0x47, 0x01, 0x00, 0x10}, 64*1024) // 256 KiB
	done := make(chan struct{})
	ready := make(chan struct{})
	close(ready)

	hasBFrames := true
	session := &bitrateMeasuringSession{
		done:        done,
		sourceReady: ready,
		broadcaster: &models.Broadcaster{
			StreamDetails: models.InboundStreamDetails{
				VideoCodec: "H.264",
				Width:      1920,
				Height:     1080,
				HasBFrames: &hasBFrames,
			},
		},
	}
	src := &inspectingChunkReader{
		data:      payload,
		threshold: 64 * 1024,
		onReached: func() {
			session.SetBroadcaster(&models.Broadcaster{
				StreamDetails: models.InboundStreamDetails{
					VideoCodec:         "H.264",
					Width:              1920,
					Height:             1080,
					HasBFrames:         &hasBFrames,
					VideoBitrate:       4800,
					VideoBitrateSource: models.VideoBitrateMeasured,
				},
			})
		},
	}
	session.reader = src

	var mediaReader io.Reader = src
	svc := &Service{}
	start := time.Now()
	info, ok := svc.awaitSourceBitrate(session, &mediaReader)
	elapsed := time.Since(start)

	if !ok {
		t.Fatal("awaitSourceBitrate returned false even though reads produced a bitrate")
	}
	if info.VideoBitrateKbps != 4800 {
		t.Fatalf("VideoBitrateKbps = %d, want 4800", info.VideoBitrateKbps)
	}
	if elapsed > time.Second {
		t.Fatalf("awaitSourceBitrate took %s, expected fast return once bitrate landed", elapsed)
	}

	replayed, err := io.ReadAll(mediaReader)
	if err != nil {
		t.Fatalf("draining mediaReader failed: %v", err)
	}
	if !bytes.Equal(replayed, payload) {
		t.Fatalf("replayed %d bytes (match=%v), want all %d bytes intact", len(replayed), bytes.Equal(replayed, payload), len(payload))
	}
}

// TestAwaitSourceBitrateAdvancesPastExistingPrefetchHead proves that when
// mediaReader already wraps a prefetchReader holding >50% of the memory ceiling,
// awaitSourceBitrate still drains through it to pull fresh bytes from the source
// instead of hitting a halved budget before reading anything new.
func TestAwaitSourceBitrateAdvancesPastExistingPrefetchHead(t *testing.T) {
	// Pre-populate an outer prefetchReader with > 16 MiB (over half of 32 MiB).
	headSize := (webRTCReadAheadMaxBytes / 2) + 1024*1024 // 17 MiB
	tailSize := 256 * 1024                                // 256 KiB fresh bytes
	head := bytes.Repeat([]byte{0x47}, headSize)
	tail := bytes.Repeat([]byte{0x55}, tailSize)
	payload := append(head, tail...)

	done := make(chan struct{})
	ready := make(chan struct{})
	close(ready)

	hasBFrames := true
	session := &bitrateMeasuringSession{
		done:        done,
		sourceReady: ready,
		broadcaster: &models.Broadcaster{
			StreamDetails: models.InboundStreamDetails{
				VideoCodec: "H.264",
				Width:      1920,
				Height:     1080,
				HasBFrames: &hasBFrames,
			},
		},
	}
	src := &inspectingChunkReader{
		data:      payload,
		threshold: headSize + 64*1024, // Only triggers once fresh bytes past the 17 MiB head are read.
		onReached: func() {
			session.SetBroadcaster(&models.Broadcaster{
				StreamDetails: models.InboundStreamDetails{
					VideoCodec:         "H.264",
					Width:              1920,
					Height:             1080,
					HasBFrames:         &hasBFrames,
					VideoBitrate:       6200,
					VideoBitrateSource: models.VideoBitrateMeasured,
				},
			})
		},
	}
	session.reader = src

	firstPrefetch := newPrefetchReader(src)
	firstPrefetch.Prefetch(time.Now().Add(2*time.Second), headSize)
	<-firstPrefetch.BudgetSpent()

	var mediaReader io.Reader = firstPrefetch
	svc := &Service{}
	info, ok := svc.awaitSourceBitrate(session, &mediaReader)
	if !ok || info.VideoBitrateKbps != 6200 {
		t.Fatalf("awaitSourceBitrate = (%d, %v), want (6200, true)", info.VideoBitrateKbps, ok)
	}

	replayed, err := io.ReadAll(mediaReader)
	if err != nil {
		t.Fatalf("draining mediaReader failed: %v", err)
	}
	if len(replayed) != len(payload) || !bytes.Equal(replayed, payload) {
		t.Fatalf("replayed %d bytes (match=%v), want %d bytes intact", len(replayed), bytes.Equal(replayed, payload), len(payload))
	}
}

// TestIngestTeeAttempt0HeadBudgetPreventsSpuriousOverflow proves that a
// prefetched head larger than the 4 MiB optional branch budget does not
// overflow Attempt 0 while the WebRTC process starts up.
func TestIngestTeeAttempt0HeadBudgetPreventsSpuriousOverflow(t *testing.T) {
	// 6 MiB of valid MPEG-TS sync packets (> 4 MiB default teeBranchQueueBytes).
	packet := make([]byte, 188)
	packet[0] = 0x47
	const packets = 34000 // ~6.39 MiB
	payload := bytes.Repeat(packet, packets)

	pr := newPrefetchReader(bytes.NewReader(payload))
	pr.Prefetch(time.Now().Add(2*time.Second), webRTCReadAheadMaxBytes)
	deadline := time.Now().Add(2 * time.Second)
	for pr.BufferedBytes() < len(payload) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if pr.BufferedBytes() <= teeBranchQueueBytes {
		t.Fatalf("expected prefetched head > %d bytes, got %d", teeBranchQueueBytes, pr.BufferedBytes())
	}

	tee := newIngestTee(pr)
	hls := tee.AddBranch("hls", true, nil)

	var overflowed atomic.Int32
	headBudget := int64(bufferedPrefetchBytes(pr))
	wrtc := tee.AddLateBranchWithBudget("webrtc", false, func() { overflowed.Add(1) }, "mpegts", nil, headBudget)

	tee.Start()

	// HLS begins reading immediately; WebRTC waits 50ms to simulate FFmpeg exec startup.
	hlsDone := make(chan int, 1)
	go func() {
		n, _ := io.Copy(io.Discard, hls)
		hlsDone <- int(n)
	}()

	time.Sleep(50 * time.Millisecond)
	wrtcData, err := io.ReadAll(wrtc)
	if err != nil {
		t.Fatalf("Attempt 0 WebRTC branch failed during head replay: %v", err)
	}
	if overflowed.Load() != 0 {
		t.Fatal("Attempt 0 WebRTC branch spuriously overflowed on prefetched head")
	}
	if len(wrtcData) != len(payload) {
		t.Fatalf("WebRTC branch received %d bytes, want %d", len(wrtcData), len(payload))
	}
	if gotHLS := <-hlsDone; gotHLS != len(payload) {
		t.Fatalf("HLS branch received %d bytes, want %d", gotHLS, len(payload))
	}
}

// TestEffectiveWebRTCVariantsAndReplanPreserveBroadcastAndDiagnostics proves
// that effectiveWebRTCVariants deep-copies pointer fields and that a second
// resolveWebRTCLegs pass keeps diagnostics for copyFallback=disable variants.
func TestEffectiveWebRTCVariantsAndReplanPreserveBroadcastAndDiagnostics(t *testing.T) {
	original := []models.StreamOutputVariant{
		{
			Name:               "Source Disable",
			IsVideoPassthrough: true,
			Outputs: &models.VariantOutputs{
				HLS: &models.ProtocolOutputSettings{Enabled: models.BoolPtr(true)},
				WebRTC: &models.ProtocolOutputSettings{
					Enabled:      models.BoolPtr(true),
					Mode:         models.OutputModeCopy,
					CopyFallback: models.CopyFallbackDisable,
				},
			},
		},
		{
			Name:               "Source Transcode",
			IsVideoPassthrough: true,
			Outputs: &models.VariantOutputs{
				HLS: &models.ProtocolOutputSettings{Enabled: models.BoolPtr(true)},
				WebRTC: &models.ProtocolOutputSettings{
					Enabled:      models.BoolPtr(true),
					Mode:         models.OutputModeCopy,
					CopyFallback: models.CopyFallbackTranscode,
				},
			},
		},
	}

	svc := &Service{}
	cfg := models.DefaultWebRTCConfig()
	hasBFrames := true
	source := webrtc.SourceInfo{
		Protocol:     "srt",
		VideoCodec:   "H.264",
		VideoProfile: "high",
		VideoLevel:   "4.1",
		Width:        1920,
		Height:       1080,
		FPS:          30,
		HasBFrames:   &hasBFrames,
	}

	webrtcVariants := effectiveWebRTCVariants(original)
	decisions1, legs1, _, hasEnabled1 := svc.resolveWebRTCLegs(webrtcVariants, cfg, source)
	if !hasEnabled1 || len(legs1) != 2 {
		t.Fatalf("first pass: hasEnabled=%v len(legs)=%d, want true/2", hasEnabled1, len(legs1))
	}
	if !needsMeasuredBitrate(decisions1) {
		t.Fatal("expected second variant to need measured bitrate")
	}

	// Second pass after bitrate measurement arrives must still include variant 0's disabled status.
	source.VideoBitrateKbps = 5000
	source.BitrateSource = models.VideoBitrateMeasured
	decisions2, legs2, _, hasEnabled2 := svc.resolveWebRTCLegs(webrtcVariants, cfg, source)
	if !hasEnabled2 || len(legs2) != 2 {
		t.Fatalf("second pass lost disabled variant diagnostics: hasEnabled=%v len(legs)=%d", hasEnabled2, len(legs2))
	}
	if legs2[0].Mode != models.WebRTCLegModeDisabled {
		t.Fatalf("legs2[0].Mode = %q, want disabled", legs2[0].Mode)
	}

	published := disableUnplannedWebRTCVariants(webrtcVariants, decisions2)
	if published[0].WebRTCEnabled() {
		t.Fatal("disabled variant must be disabled on published copy")
	}
	if !published[1].WebRTCEnabled() {
		t.Fatal("enabled variant must stay enabled on published copy")
	}
	// Original broadcast OutputSettings must never be mutated.
	if !original[0].WebRTCEnabled() {
		t.Fatal("broadcast OutputSettings[0] was mutated in place")
	}
}

func TestHLSVariantAtResolvesSequentialHLSIndexWhenEarlierVariantDisablesHLS(t *testing.T) {
	hlsOff := models.StreamOutputVariant{
		Name:         "WebRTC Only",
		VideoBitrate: 6000,
		Outputs: &models.VariantOutputs{
			HLS:    &models.ProtocolOutputSettings{Enabled: models.BoolPtr(false)},
			WebRTC: &models.ProtocolOutputSettings{Enabled: models.BoolPtr(true)},
		},
	}
	hls0 := models.StreamOutputVariant{Name: "HLS 720p", VideoBitrate: 2500}
	hls1 := models.StreamOutputVariant{Name: "HLS 480p", VideoBitrate: 1000}
	all := []models.StreamOutputVariant{hlsOff, hls0, hls1}

	enabled := hlsEnabledVariants(all)
	if len(enabled) != 2 || enabled[0].Name != "HLS 720p" || enabled[1].Name != "HLS 480p" {
		t.Fatalf("hlsEnabledVariants = %+v", enabled)
	}

	v0, ok := hlsVariantAt(all, 0)
	if !ok || v0.Name != "HLS 720p" {
		t.Fatalf("hlsVariantAt(0) = (%q, %v), want (HLS 720p, true)", v0.Name, ok)
	}
	v1, ok := hlsVariantAt(all, 1)
	if !ok || v1.Name != "HLS 480p" {
		t.Fatalf("hlsVariantAt(1) = (%q, %v), want (HLS 480p, true)", v1.Name, ok)
	}
	if _, ok := hlsVariantAt(all, 2); ok {
		t.Fatal("hlsVariantAt(2) must return false when only two variants enable HLS")
	}
}
