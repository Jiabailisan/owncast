package livestatus

import (
	"testing"
	"time"

	"github.com/owncast/owncast/models"
)

// TestPaginateLiveViewersClampsHugePage is the regression test for a crash: page
// and pageSize come from the query string, and (page-1)*pageSize overflowed into
// negative slice bounds.
func TestPaginateLiveViewersClampsHugePage(t *testing.T) {
	viewers := []models.LiveViewer{{ClientID: "a"}, {ClientID: "b"}}

	page, pageSize := normalizeViewerPage(1<<31-1, 1<<31-1)
	result := paginateLiveViewers(viewers, page, pageSize)

	if result.Total != len(viewers) {
		t.Fatalf("total should stay %d, got %d", len(viewers), result.Total)
	}
	if len(result.Viewers) != 0 {
		t.Fatalf("a page past the end should be empty, got %d rows", len(result.Viewers))
	}
}

// TestPaginateLiveViewersKeepsNormalPages proves the clamp did not break paging.
func TestPaginateLiveViewersKeepsNormalPages(t *testing.T) {
	viewers := make([]models.LiveViewer, 5)
	for index := range viewers {
		viewers[index].ClientID = string(rune('a' + index))
	}

	result := paginateLiveViewers(viewers, 2, 2)
	if len(result.Viewers) != 2 || result.Viewers[0].ClientID != "c" {
		t.Fatalf("unexpected page contents: %+v", result.Viewers)
	}
}

// TestSortLiveViewersByBitrate is the regression test for a table column that
// offered a sorter with no backend branch, so clicking it changed nothing.
func TestSortLiveViewersByBitrate(t *testing.T) {
	low, high := 900.0, 6000.0
	viewers := []models.LiveViewer{
		{ClientID: "none", BitrateKbps: nil},
		{ClientID: "low", BitrateKbps: &low},
		{ClientID: "high", BitrateKbps: &high},
	}

	sortLiveViewers(viewers, "bitrate", false)
	if viewers[0].ClientID != "high" || viewers[1].ClientID != "low" {
		t.Fatalf("descending bitrate order is wrong: %s, %s", viewers[0].ClientID, viewers[1].ClientID)
	}
	if viewers[2].ClientID != "none" {
		t.Fatalf("a row without a measurement should sort last, got %s", viewers[2].ClientID)
	}

	sortLiveViewers(viewers, "bitrate", true)
	if viewers[0].ClientID != "low" || viewers[1].ClientID != "high" {
		t.Fatalf("ascending bitrate order is wrong: %s, %s", viewers[0].ClientID, viewers[1].ClientID)
	}
}

// TestSortLiveViewersByDurationStillWorks guards the default paths against a
// regression from adding the bitrate branch.
func TestSortLiveViewersByDurationStillWorks(t *testing.T) {
	viewers := []models.LiveViewer{
		{ClientID: "new", DurationSeconds: 5, ConnectedAt: time.Now()},
		{ClientID: "old", DurationSeconds: 500, ConnectedAt: time.Now().Add(-time.Hour)},
	}

	sortLiveViewers(viewers, "duration", false)
	if viewers[0].ClientID != "old" {
		t.Fatalf("expected the longest duration first, got %s", viewers[0].ClientID)
	}
}
