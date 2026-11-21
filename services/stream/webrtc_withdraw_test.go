package stream

import (
	"bytes"
	"sync"
	"testing"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/transcoder"
)

// recordingProvider records the withdrawals the supervisor asks for.
type recordingProvider struct {
	mu      sync.Mutex
	gens    []uint64
	indices [][]int
}

func (r *recordingProvider) SetVariants([]models.StreamOutputVariant) (uint64, error) { return 0, nil }

func (r *recordingProvider) OpenMediaLeg(int, uint64) (WebRTCMediaLeg, error) { return nil, nil }
func (r *recordingProvider) CloseMediaLeg(int) error                          { return nil }
func (r *recordingProvider) VariantGeneration() uint64                        { return 0 }

func (r *recordingProvider) DisableVariants(generation uint64, indices []int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gens = append(r.gens, generation)
	r.indices = append(r.indices, append([]int(nil), indices...))
}

// A clean exit and an exhausted retry budget both withdraw this stream's renditions
// and release its branch.
func TestWithdrawWebRTCVariantsRemovesTheStreamRenditions(t *testing.T) {
	provider := &recordingProvider{}
	svc := &Service{webRTC: provider}
	tee := newIngestTee(bytes.NewReader(nil))
	reader := tee.AddBranch(webRTCBranchName, false, nil)
	session := newTestIngestSession("stream")
	svc.claimWebRTCProcessSession(session)

	svc.withdrawWebRTCVariants(tee, session, 7, []transcoder.RTPOutput{
		{VariantIndex: 0}, {VariantIndex: 1},
	})

	provider.mu.Lock()
	gens, indices := provider.gens, provider.indices
	provider.mu.Unlock()

	if len(gens) != 1 || gens[0] != 7 {
		t.Fatalf("withdrawals = %v, want one at generation 7", gens)
	}
	if len(indices) != 1 || len(indices[0]) != 2 {
		t.Fatalf("withdrawn indices = %v, want the two RTP outputs", indices)
	}

	// The branch was removed, so a read returns the close error instead of blocking.
	if _, err := reader.Read(make([]byte, 1)); err == nil {
		t.Fatal("the branch must be removed")
	}
}

// The failure warning belongs to the stream that owns the WebRTC state.
func TestWarningIsOnlyRecordedForTheOwningGeneration(t *testing.T) {
	svc := &Service{}
	svc.webRTCVariantGen = 5

	svc.setWebRTCWarningsForGeneration(4, []models.StatusWarning{{Code: "stale"}})
	if got := svc.WebRTCWarnings(); len(got) != 0 {
		t.Fatalf("a stale supervisor recorded %d warnings", len(got))
	}

	svc.setWebRTCWarningsForGeneration(5, []models.StatusWarning{{Code: "current"}})
	if got := svc.WebRTCWarnings(); len(got) != 1 {
		t.Fatalf("the owning stream recorded %d warnings, want 1", len(got))
	}
}
