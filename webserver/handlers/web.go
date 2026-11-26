package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"strings"
	"sync"

	"github.com/owncast/owncast/static"
)

var (
	staticWebFS  = static.GetWeb()
	staticServer = http.FileServer(http.FS(staticWebFS))

	staticBundleETagOnce sync.Once
	staticBundleETag     string
)

// embeddedBundleETag computes a stable weak ETag derived from the embedded
// index.html bytes so embedded static files (whose embed.FS ModTime is zero)
// still support conditional 304 Not Modified revalidation across requests.
func embeddedBundleETag() string {
	staticBundleETagOnce.Do(func() {
		data, err := fs.ReadFile(staticWebFS, "index.html")
		if err != nil {
			return
		}
		sum := sha256.Sum256(data)
		staticBundleETag = `W/"` + hex.EncodeToString(sum[:8]) + `"`
	})
	return staticBundleETag
}

// serveWeb will serve web assets.
func serveWeb(w http.ResponseWriter, r *http.Request) {
	// Content-hashed Next.js chunks under /_next/static/chunks/ are immutable.
	if strings.HasPrefix(r.URL.Path, "/_next/static/chunks/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}

	if etag := embeddedBundleETag(); etag != "" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		w.Header().Set("ETag", etag)
		if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}

	staticServer.ServeHTTP(w, r)
}
