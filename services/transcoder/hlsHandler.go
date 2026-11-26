package transcoder

import (
	"github.com/owncast/owncast/models"
)

// HLSHandler gets told about available HLS playlists and segments.
type HLSHandler struct {
	Storage          models.StorageProvider
	OnSegmentWritten func(localFilePath string)
}

// SegmentWritten is fired when a HLS segment is written to disk.
func (h *HLSHandler) SegmentWritten(localFilePath string) {
	if h.OnSegmentWritten != nil {
		h.OnSegmentWritten(localFilePath)
	}
	h.Storage.SegmentWritten(localFilePath)
}

// VariantPlaylistWritten is fired when a HLS variant playlist is written to disk.
func (h *HLSHandler) VariantPlaylistWritten(localFilePath string) {
	h.Storage.VariantPlaylistWritten(localFilePath)
}

// MasterPlaylistWritten is fired when a HLS master playlist is written to disk.
func (h *HLSHandler) MasterPlaylistWritten(localFilePath string) {
	h.Storage.MasterPlaylistWritten(localFilePath)
}
