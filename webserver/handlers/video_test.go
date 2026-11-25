package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/persistence/configrepository"
)

type stubVariantsConfigRepo struct {
	configrepository.ConfigRepository
	variants []models.StreamOutputVariant
}

func (s *stubVariantsConfigRepo) GetStreamOutputVariants() []models.StreamOutputVariant {
	return s.variants
}

func TestGetVideoStreamOutputVariantsExcludesHLSDisabledAndRenumbers(t *testing.T) {
	h := &Handlers{
		configRepository: &stubVariantsConfigRepo{
			variants: []models.StreamOutputVariant{
				{
					Name:               "WebRTC Passthrough Only",
					IsVideoPassthrough: true,
					Outputs: &models.VariantOutputs{
						HLS:    &models.ProtocolOutputSettings{Enabled: models.BoolPtr(false)},
						WebRTC: &models.ProtocolOutputSettings{Enabled: models.BoolPtr(true)},
					},
				},
				{Name: "Low 1Mbps", VideoBitrate: 1000},
				{Name: "High 4Mbps", VideoBitrate: 4000},
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/video/variants", nil)
	rec := httptest.NewRecorder()
	h.GetVideoStreamOutputVariants(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got []variantsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(variants) = %d, want 2 (excluding HLS-disabled variant): %+v", len(got), got)
	}
	// Sorted descending by bitrate, while indices match sequential FFmpeg /hls/<index>/ directories.
	if got[0].Name != "High 4Mbps" || got[0].Index != 1 {
		t.Fatalf("got[0] = %+v, want {Name: High 4Mbps, Index: 1}", got[0])
	}
	if got[1].Name != "Low 1Mbps" || got[1].Index != 0 {
		t.Fatalf("got[1] = %+v, want {Name: Low 1Mbps, Index: 0}", got[1])
	}
}
