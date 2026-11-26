package static

import (
	"embed"
	"html/template"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/pkg/errors"
)

// The 'all:' prefix is used to include all files and subdirectories under 'web/'.
// This avoids errors if optional subdirectories (e.g., 'web/_next') are missing.
//
//go:embed all:web/*
var webFiles embed.FS

// GetWeb will return an embedded filesystem reference to the admin web app.
func GetWeb() fs.FS {
	wf, err := fs.Sub(webFiles, "web")
	if err != nil {
		log.Fatal(err)
	}
	return wf
}

//go:embed img/emoji/*
var emojiFiles embed.FS

// GetEmoji will return the emoji files.
func GetEmoji() fs.FS {
	ef, err := fs.Sub(emojiFiles, "img/emoji")
	if err != nil {
		log.Fatal(err)
	}
	return ef
}

var (
	webIndexTemplateOnce sync.Once
	webIndexTemplate     *template.Template
	webIndexTemplateErr  error

	botMetadataTemplateOnce sync.Once
	botMetadataParsed       *template.Template
	botMetadataErr          error
)

// GetWebIndexTemplate will return the compiled embedded index.html template,
// parsed once and cached for the lifetime of the process.
func GetWebIndexTemplate() (*template.Template, error) {
	webIndexTemplateOnce.Do(func() {
		wf := GetWeb()
		t, err := template.ParseFS(wf, "index.html")
		if err != nil {
			webIndexTemplateErr = errors.Wrap(err, "unable to open index.html template")
			return
		}
		webIndexTemplate = t
	})
	return webIndexTemplate, webIndexTemplateErr
}

//go:embed offline-v2.ts
var offlineVideoSegment []byte

// GetOfflineSegment will return the offline video segment data.
func GetOfflineSegment() []byte {
	return getFileSystemStaticFileOrDefault("offline-v2.ts", offlineVideoSegment)
}

//go:embed img/logo.png
var logo []byte

// GetLogo will return the logo data.
func GetLogo() []byte {
	return getFileSystemStaticFileOrDefault("img/logo.png", logo)
}

//go:embed favicon.png
var favicon []byte

// GetFavicon will return the favicon data.
func GetFavicon() []byte {
	return getFileSystemStaticFileOrDefault("favicon.png", favicon)
}

func getFileSystemStaticFileOrDefault(path string, defaultData []byte) []byte {
	fullPath := filepath.Join("static", path)
	data, err := os.ReadFile(fullPath) //nolint: gosec
	if err != nil {
		return defaultData
	}

	return data
}

//go:embed metadata.html.tmpl
var botMetadataTemplate embed.FS

// GetBotMetadataTemplate will return the bot/scraper metadata template,
// parsed once and cached for the lifetime of the process.
func GetBotMetadataTemplate() (*template.Template, error) {
	botMetadataTemplateOnce.Do(func() {
		t, err := template.ParseFS(botMetadataTemplate, "metadata.html.tmpl")
		if err != nil {
			botMetadataErr = errors.Wrap(err, "unable to open metadata.html.tmpl template")
			return
		}
		botMetadataParsed = t
	})
	return botMetadataParsed, botMetadataErr
}
