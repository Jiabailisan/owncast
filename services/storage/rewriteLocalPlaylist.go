package storage

import (
	"bufio"
	"os"
	"path/filepath"

	"github.com/grafov/m3u8"

	"github.com/owncast/owncast/config"
)

// rewritePlaylistLocations will take a local playlist and rewrite it to have absolute URLs to a specified location.
func rewritePlaylistLocations(localFilePath, remoteServingEndpoint, hlsPrefix string) error {
	f, err := os.Open(localFilePath) // nolint
	if err != nil {
		// A momentarily missing playlist must not kill the server.
		return err
	}
	defer f.Close()

	p := m3u8.NewMasterPlaylist()
	if err := p.DecodeFrom(bufio.NewReader(f), false); err != nil {
		// Never write a partially decoded playlist back over the live one.
		return err
	}

	if hlsPrefix == "" {
		hlsPrefix = "hls"
	}

	for _, item := range p.Variants {
		item.URI = remoteServingEndpoint + "/" + hlsPrefix + "/" + item.URI
	}

	publicPath := filepath.Join(config.HLSStoragePath, filepath.Base(localFilePath))

	newPlaylist := p.String()

	return writePlaylist(newPlaylist, publicPath)
}
