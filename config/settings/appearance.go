package settings

// Appearance/frontend customization resolution (master spec 4.6, 7.3, 7.4).
//
// The precedence is the single source of truth used by the viewer page, the
// custom-player endpoint, the public config API and the admin Appearance page:
//
//  1. customFrontendDir set and valid  -> serve that viewer frontend; ignore
//     customPlayerPath and never inject or serve Custom CSS/JavaScript;
//  2. else customPlayerPath set and valid -> default frontend with the custom
//     player module;
//  3. else -> the default frontend with the existing Custom CSS/JavaScript.
//
// A configured path that does not validate never silently falls through to a
// less specific mode without leaving the invalid state visible to the admin:
// the validation error is part of the resolved state.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/owncast/owncast/config"
)

// FrontendMode is the active viewer frontend mode.
type FrontendMode string

const (
	// FrontendDefault is the built-in viewer frontend.
	FrontendDefault FrontendMode = "default"
	// FrontendCustomPlayer is the default frontend with a custom player module.
	FrontendCustomPlayer FrontendMode = "custom-player"
	// FrontendCustomFrontend is a fully custom viewer frontend directory.
	FrontendCustomFrontend FrontendMode = "custom-frontend"
)

// AppearancePaths is the persisted appearance configuration input.
type AppearancePaths struct {
	// CustomPlayerPath is the configured ES player module path ("" = none).
	CustomPlayerPath string
	// CustomFrontendDir is the configured custom viewer directory ("" = none).
	CustomFrontendDir string
}

// AppearanceState is the resolved precedence result. Both configured values are
// validated even when the precedence ignores one of them, so the admin UI can
// explain why a value is ignored or invalid.
type AppearanceState struct {
	// Mode is the active frontend mode.
	Mode FrontendMode
	// PlayerPath is the resolved absolute player module path ("" unless active).
	PlayerPath string
	// FrontendDir is the resolved absolute viewer directory ("" unless active).
	FrontendDir string
	// PlayerValid reports whether the configured player path validates.
	PlayerValid bool
	// PlayerError explains why the configured player path is invalid.
	PlayerError string
	// FrontendValid reports whether the configured frontend directory validates.
	FrontendValid bool
	// FrontendError explains why the configured frontend directory is invalid.
	FrontendError string
}

// Active reports whether a customization is active.
func (s AppearanceState) Active() bool {
	return s.Mode != FrontendDefault
}

// CustomCSSAndJSServed reports whether the legacy Custom CSS/JavaScript
// injection is active. A custom frontend replaces the viewer and therefore
// disables it (spec 4.6 step 1).
func (s AppearanceState) CustomCSSAndJSServed() bool {
	return s.Mode != FrontendCustomFrontend
}

// ResolveAppearance applies the spec 4.6 precedence and validates both
// configured paths. It never returns an error: invalid configuration is
// represented in the state so it can be surfaced to the admin and the service
// can safely fall back.
func ResolveAppearance(paths AppearancePaths) AppearanceState {
	state := AppearanceState{Mode: FrontendDefault}

	if paths.CustomFrontendDir != "" {
		directory, err := ValidateFrontendDir(paths.CustomFrontendDir)
		if err != nil {
			state.FrontendError = err.Error()
		} else {
			state.FrontendValid = true
			state.FrontendDir = directory
		}
	}
	if paths.CustomPlayerPath != "" {
		path, err := ValidatePlayerPath(paths.CustomPlayerPath)
		if err != nil {
			state.PlayerError = err.Error()
		} else {
			state.PlayerValid = true
			state.PlayerPath = path
		}
	}

	switch {
	case state.FrontendValid:
		state.Mode = FrontendCustomFrontend
		// The custom frontend wins entirely; the player is ignored.
		state.PlayerPath = ""
		state.PlayerValid = false
	case state.PlayerValid:
		state.Mode = FrontendCustomPlayer
	}

	return state
}

// IsValid reports whether the state would be used by the viewer.
func (s AppearanceState) IsValid() bool {
	return s.Mode != FrontendDefault
}

var errPathMustBeAbsolute = errors.New("path must be absolute")

// ValidatePlayerPath validates a custom player module path and returns its
// cleaned, symlink-resolved absolute form. It rejects directories, unreadable
// files, relative paths, paths inside Owncast's private data directory and
// symlinks that escape validation.
func ValidatePlayerPath(path string) (string, error) {
	resolved, err := validateAppearancePath(path, "custom player path")
	if err != nil {
		return "", err
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("custom player path is not accessible: %s", err.Error())
	}
	if info.IsDir() {
		return "", errors.New("custom player path must be a file, not a directory")
	}

	file, err := os.Open(resolved) //nolint:gosec
	if err != nil {
		return "", fmt.Errorf("custom player path is not readable: %s", err.Error())
	}
	_ = file.Close()

	return resolved, nil
}

// ValidateFrontendDir validates a custom viewer frontend directory and returns
// its cleaned, symlink-resolved absolute form. The directory must contain a
// readable index.html and must not overlap Owncast's private data directory.
func ValidateFrontendDir(directory string) (string, error) {
	resolved, err := validateAppearancePath(directory, "custom frontend directory")
	if err != nil {
		return "", err
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("custom frontend directory is not accessible: %s", err.Error())
	}
	if !info.IsDir() {
		return "", errors.New("custom frontend directory must be a directory")
	}

	indexPath := filepath.Join(resolved, "index.html")
	indexInfo, err := os.Stat(indexPath)
	if err != nil {
		return "", errors.New("custom frontend directory must contain an index.html")
	}
	if indexInfo.IsDir() {
		return "", errors.New("custom frontend directory index.html must be a file")
	}
	indexFile, err := os.Open(indexPath) //nolint:gosec
	if err != nil {
		return "", fmt.Errorf("custom frontend directory index.html is not readable: %s", err.Error())
	}
	_ = indexFile.Close()

	return resolved, nil
}

// validateAppearancePath performs the shared absolute/symlink/private-directory
// checks and returns the resolved absolute path.
func validateAppearancePath(path, label string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", fmt.Errorf("%s is empty", label)
	}
	if !filepath.IsAbs(trimmed) {
		return "", fmt.Errorf("%s %w", label, errPathMustBeAbsolute)
	}

	cleaned := filepath.Clean(trimmed)
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		return "", fmt.Errorf("%s does not exist or is not accessible: %s", label, err.Error())
	}

	if isInsidePrivateDataDirectory(resolved) {
		return "", fmt.Errorf("%s must not be inside Owncast's private data directory", label)
	}

	return resolved, nil
}

// isInsidePrivateDataDirectory reports whether path is the data directory or
// inside it (database, HLS output, logs, temp files, backups).
func isInsidePrivateDataDirectory(path string) bool {
	dataDir, err := filepath.Abs(config.DataDirectory)
	if err != nil {
		return false
	}
	resolvedDataDir, err := filepath.EvalSymlinks(dataDir)
	if err != nil {
		resolvedDataDir = dataDir
	}

	relative, err := filepath.Rel(resolvedDataDir, path)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}
