package handlers

// Viewer frontend customization: custom player module serving and the custom
// viewer frontend directory (master spec 4.6, 7.3, 7.4).
//
// The precedence function lives in config/settings (ResolveAppearance) and is
// shared by this serving path, the public config API and the admin Appearance
// report, so the three can never disagree.

import (
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/owncast/owncast/config/settings"
)

// customPlayerModulePath is the public URL of the custom player module.
const customPlayerModulePath = "/custom-player/player.js"

// resolveAppearance validates and resolves the configured appearance paths.
// It is nil-safe so partial handler sets used by tests behave like an
// unconfigured server.
func (h *Handlers) resolveAppearance() settings.AppearanceState {
	if h.configRepository == nil {
		return settings.ResolveAppearance(settings.AppearancePaths{})
	}
	return settings.ResolveAppearance(settings.AppearancePaths{
		CustomPlayerPath:  h.configRepository.GetCustomPlayerPath(),
		CustomFrontendDir: h.configRepository.GetCustomFrontendDir(),
	})
}

// ServeCustomPlayer serves the configured custom player ES module read-only.
// It returns 404 whenever the custom player is not the active mode.
func (h *Handlers) ServeCustomPlayer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	state := h.resolveAppearance()
	if state.Mode != settings.FrontendCustomPlayer || state.PlayerPath == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	file, err := os.Open(state.PlayerPath) //nolint:gosec
	if err != nil {
		log.Warnln("unable to open the configured custom player path:", err)
		w.WriteHeader(http.StatusNotFound)
		return
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil || info.IsDir() {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, filepath.Base(state.PlayerPath), info.ModTime(), file)
}

// serveCustomFrontend serves the viewer frontend from the configured custom
// directory, with an index.html SPA fallback and strict path containment.
// It must only be called after resolving FrontendCustomFrontend mode.
func (h *Handlers) serveCustomFrontend(w http.ResponseWriter, r *http.Request, directory string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	root, err := os.OpenRoot(directory)
	if err != nil {
		log.Errorln("unable to open the custom frontend directory:", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer root.Close()

	relativePath := cleanFrontendRequestPath(r.URL.Path)
	if relativePath != "" {
		if file, info, ok := openFrontendFile(root, relativePath); ok {
			defer file.Close()
			serveFrontendFile(w, r, relativePath, info, file)
			return
		}
	}

	// SPA fallback: unknown paths serve index.html so client-side routing works.
	file, info, ok := openFrontendFile(root, "index.html")
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	defer file.Close()
	serveFrontendFile(w, r, "index.html", info, file)
}

// cleanFrontendRequestPath converts a URL path into a safe slash-separated
// relative path. Path traversal and null bytes are rejected by returning "".
func cleanFrontendRequestPath(urlPath string) string {
	if strings.ContainsAny(urlPath, "\\\x00\r\n") {
		return ""
	}
	cleaned := path.Clean("/" + strings.TrimPrefix(urlPath, "/"))
	relativePath := strings.TrimPrefix(cleaned, "/")
	if relativePath == "." {
		return ""
	}
	for _, segment := range strings.Split(relativePath, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return ""
		}
	}
	return relativePath
}

// openFrontendFile opens a file inside the custom frontend root. Directories
// are resolved to their index.html. Returns ok=false when the path is not a
// regular file inside the root.
func openFrontendFile(root *os.Root, relativePath string) (*os.File, os.FileInfo, bool) {
	file, err := root.Open(filepath.FromSlash(relativePath))
	if err != nil {
		return nil, nil, false
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, false
	}
	if info.IsDir() {
		_ = file.Close()
		indexPath := path.Join(relativePath, "index.html")
		indexFile, indexErr := root.Open(filepath.FromSlash(indexPath))
		if indexErr != nil {
			return nil, nil, false
		}
		indexInfo, indexErr := indexFile.Stat()
		if indexErr != nil || indexInfo.IsDir() {
			_ = indexFile.Close()
			return nil, nil, false
		}
		return indexFile, indexInfo, true
	}
	return file, info, true
}

// serveFrontendFile writes one custom frontend file with a content type derived
// from its extension.
func serveFrontendFile(w http.ResponseWriter, r *http.Request, name string, info os.FileInfo, file *os.File) {
	contentType := mime.TypeByExtension(filepath.Ext(name))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}
