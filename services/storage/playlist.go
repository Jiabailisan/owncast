package storage

import (
	"os"
	"path/filepath"
)

// writePlaylist writes an HLS playlist atomically to disk via temp file + rename
// so concurrent readers never observe an empty or half-written playlist.
func writePlaylist(data string, filePath string) error {
	dir := filepath.Dir(filePath)
	tmp, err := os.CreateTemp(dir, ".playlist-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.WriteString(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, filePath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}
