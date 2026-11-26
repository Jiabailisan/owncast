package handlers

import (
	"net/http"
	"path/filepath"
	"time"

	"github.com/owncast/owncast/utils"
)

const (
	contentTypeJPEG = "image/jpeg"
	contentTypeGIF  = "image/gif"

	thumbnailFilename = "thumbnail.jpg"
)

// GetThumbnail will return the thumbnail image as a response.
func (h *Handlers) GetThumbnail(w http.ResponseWriter, r *http.Request) {
	imageFilename := thumbnailFilename
	imagePath := filepath.Join(h.cfg.TempDir, imageFilename)
	httpCacheTime := utils.GetCacheDurationSecondsForPath(imagePath)
	inMemoryCacheTime := time.Duration(15) * time.Second

	var imageBytes []byte
	var err error

	if ci := h.previewThumbCache.Get(imagePath); ci != nil {
		imageBytes = ci.Value()
	} else if utils.DoesFileExists(imagePath) {
		imageBytes, err = getImage(imagePath)
		if err == nil && len(imageBytes) > 0 {
			h.previewThumbCache.Set(imagePath, imageBytes, inMemoryCacheTime)
		}
	} else {
		h.GetLogo(w, r)
		return
	}

	if err != nil {
		h.GetLogo(w, r)
		return
	}

	writeBytesAsImage(imageBytes, contentTypeJPEG, w, httpCacheTime)
}

// GetPreview will return the preview gif as a response.
func (h *Handlers) GetPreview(w http.ResponseWriter, r *http.Request) {
	imageFilename := "preview.gif"
	imagePath := filepath.Join(h.cfg.TempDir, imageFilename)
	httpCacheTime := utils.GetCacheDurationSecondsForPath(imagePath)
	inMemoryCacheTime := time.Duration(15) * time.Second

	var imageBytes []byte
	var err error

	if ci := h.previewThumbCache.Get(imagePath); ci != nil {
		imageBytes = ci.Value()
	} else if utils.DoesFileExists(imagePath) {
		imageBytes, err = getImage(imagePath)
		if err == nil && len(imageBytes) > 0 {
			h.previewThumbCache.Set(imagePath, imageBytes, inMemoryCacheTime)
		}
	} else {
		h.GetLogo(w, r)
		return
	}

	if err != nil {
		h.GetLogo(w, r)
		return
	}

	writeBytesAsImage(imageBytes, contentTypeGIF, w, httpCacheTime)
}
