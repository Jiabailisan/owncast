package stream

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/webrtc"
)

func buildValidFLVTag(tagType byte, body []byte) []byte {
	size := 11 + len(body)
	out := make([]byte, 0, size+4)
	out = append(out, tagType, byte(len(body)>>16), byte(len(body)>>8), byte(len(body)))
	out = append(out, 0, 0, 0, 0) // timestamp
	out = append(out, 0, 0, 0)    // streamID
	out = append(out, body...)
	out = append(out, byte(size>>24), byte(size>>16), byte(size>>8), byte(size))
	return out
}

func TestFLVTagBoundaryValidatesPreviousTagSize(t *testing.T) {
	// Fake candidate tag header with wrong PreviousTagSize, followed by two genuine FLV tags.
	fake := buildValidFLVTag(9, []byte{0x17, 0x01, 0x02})
	fake[len(fake)-1] ^= 0xFF // corrupt PreviousTagSize

	tag1 := buildValidFLVTag(9, []byte{0x17, 0x01, 0x00, 0x00, 0x00, 0xAA})
	tag2 := buildValidFLVTag(8, []byte{0xAF, 0x01, 0xBB, 0xCC})

	stream := append(append(append([]byte{}, fake...), tag1...), tag2...)
	offset, ok := flvTagBoundary(stream)
	if !ok {
		t.Fatal("flvTagBoundary failed to locate valid FLV boundary")
	}
	if offset != len(fake) {
		t.Fatalf("offset = %d, want %d (must reject fake tag with mismatched PreviousTagSize)", offset, len(fake))
	}
}

func TestIngestTeePendingLedgerStaysZeroAfterFLVAlignerAndEndChAligns(t *testing.T) {
	tag1 := buildValidFLVTag(9, []byte{0x17, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02})
	tag2 := buildValidFLVTag(8, []byte{0xAF, 0x01, 0x03, 0x04})
	junk := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	payload := append(append(append([]byte{}, junk...), tag1...), tag2...)

	prefix := []byte{'F', 'L', 'V', 0x01, 0x01, 0x00, 0x00, 0x00, 0x09, 0x00, 0x00, 0x00, 0x00}
	tee := newIngestTee(bytes.NewReader(payload))
	reader := tee.AddLateBranch("webrtc", false, nil, "flv", prefix)
	branch := tee.Branch("webrtc")
	tee.Start()

	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	want := append(append([]byte{}, prefix...), append(tag1, tag2...)...)
	if !bytes.Equal(got, want) {
		t.Fatalf("aligned output mismatch: got %d bytes, want %d bytes", len(got), len(want))
	}
	if pending := branch.pending.Load(); pending != 0 {
		t.Fatalf("branch.pending = %d after full drain, want 0 (aligner rewrite must not drift pending ledger)", pending)
	}
}

func TestAddBranchWithBudgetPreservesRawFLVStreamHeadWithoutDuplicatePrefix(t *testing.T) {
	seqHdr := buildValidFLVTag(8, []byte{0xAF, 0x00, 0x11, 0x90})
	audioTag := buildValidFLVTag(8, []byte{0xAF, 0x01, 0x21, 0x10})
	header := []byte{'F', 'L', 'V', 0x01, 0x05, 0x00, 0x00, 0x00, 0x09, 0x00, 0x00, 0x00, 0x00}
	payload := append(append(append([]byte{}, header...), seqHdr...), audioTag...)

	tee := newIngestTee(bytes.NewReader(payload))
	reader := tee.AddBranchWithBudget("webrtc", false, nil, 1<<20)
	tee.Start()

	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("Attempt 0 branch altered raw FLV stream head: got %d bytes, want %d bytes", len(got), len(payload))
	}
}

type stubConfigureWebRTCProvider struct {
	enabled         bool
	setVariantsErr  error
	setVariantsCall int
}

func (p *stubConfigureWebRTCProvider) Enabled() bool { return p.enabled }
func (p *stubConfigureWebRTCProvider) SetVariants([]models.StreamOutputVariant) (uint64, error) {
	p.setVariantsCall++
	if p.setVariantsErr != nil {
		return 0, p.setVariantsErr
	}
	return 1, nil
}
func (p *stubConfigureWebRTCProvider) OpenMediaLeg(int, uint64) (WebRTCMediaLeg, error) {
	return nil, nil
}
func (p *stubConfigureWebRTCProvider) CloseMediaLeg(int) error       { return nil }
func (p *stubConfigureWebRTCProvider) DisableVariants(uint64, []int) {}
func (p *stubConfigureWebRTCProvider) VariantGeneration() uint64     { return 1 }

type failOnReadReader struct{ t *testing.T }

func (r failOnReadReader) Read([]byte) (int, error) {
	r.t.Fatal("mediaReader must not be read when WebRTC delivery is disabled")
	return 0, io.EOF
}

func TestConfigureWebRTCSkipsPrefetchAndClearsStateWhenDisabled(t *testing.T) {
	provider := &stubConfigureWebRTCProvider{enabled: false}
	svc := &Service{
		configRepository: newStreamStatsTestRepo(t),
		webRTC:           provider,
	}
	svc.setWebRTCLegs([]models.WebRTCLegStatus{{VariantIndex: 0, Mode: models.WebRTCLegModeCopy}})
	svc.setWebRTCWarnings([]models.StatusWarning{{Code: "stale", Message: "stale"}})

	var reader io.Reader = failOnReadReader{t: t}
	out := svc.configureWebRTC(newTestIngestSession("disabled"), &reader, []models.StreamOutputVariant{
		{Name: "Source", IsVideoPassthrough: true},
	}, webrtc.SourceInfo{})

	if len(out.outputs) != 0 {
		t.Fatalf("outputs = %d, want 0", len(out.outputs))
	}
	if provider.setVariantsCall != 0 {
		t.Fatalf("SetVariants calls = %d, want 0 when WebRTC is disabled", provider.setVariantsCall)
	}
	if legs := svc.WebRTCLegs(); len(legs) != 0 {
		t.Fatalf("WebRTCLegs = %+v, want empty when WebRTC is disabled", legs)
	}
	if warnings := svc.WebRTCWarnings(); len(warnings) != 0 {
		t.Fatalf("WebRTCWarnings = %+v, want empty when WebRTC is disabled", warnings)
	}
}

func TestConfigureWebRTCDisablesLegsWhenSetVariantsFails(t *testing.T) {
	provider := &stubConfigureWebRTCProvider{
		enabled:        true,
		setVariantsErr: errors.New("publish failed"),
	}
	svc := &Service{
		configRepository: newStreamStatsTestRepo(t),
		webRTC:           provider,
	}

	var reader io.Reader = bytes.NewReader(nil)
	out := svc.configureWebRTC(newTestIngestSession("set-variants-err"), &reader, []models.StreamOutputVariant{
		{Name: "720p", ScaledWidth: 1280, ScaledHeight: 720, VideoBitrate: 2500, Framerate: 30},
	}, webrtc.SourceInfo{
		Protocol: "rtmp",
	})

	if len(out.outputs) != 0 {
		t.Fatalf("outputs = %d, want 0 on SetVariants error", len(out.outputs))
	}
	legs := svc.WebRTCLegs()
	if len(legs) != 1 {
		t.Fatalf("len(WebRTCLegs) = %d, want 1", len(legs))
	}
	if legs[0].Mode != models.WebRTCLegModeDisabled {
		t.Fatalf("legs[0].Mode = %q, want disabled after SetVariants error", legs[0].Mode)
	}
	if legs[0].OutputBitrateKbps != nil {
		t.Fatalf("legs[0].OutputBitrateKbps = %v, want nil when disabled", *legs[0].OutputBitrateKbps)
	}
}
