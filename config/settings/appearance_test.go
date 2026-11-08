package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func writeAppearanceFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func validPlayerPath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "player.js")
	writeAppearanceFile(t, path, "export default function mount() {}\n")
	return path
}

func validFrontendDir(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	writeAppearanceFile(t, filepath.Join(directory, "index.html"), "<html>custom</html>")
	writeAppearanceFile(t, filepath.Join(directory, "_next", "app.js"), "console.log(1)\n")
	return directory
}

func TestResolveAppearancePrecedence(t *testing.T) {
	playerPath := validPlayerPath(t)
	frontendDir := validFrontendDir(t)

	tests := []struct {
		name            string
		paths           AppearancePaths
		wantMode        FrontendMode
		wantCSSJS       bool
		wantPlayerValid bool
		wantFrontValid  bool
		wantPlayerError bool
		wantFrontError  bool
	}{
		{
			name:      "no customization",
			paths:     AppearancePaths{},
			wantMode:  FrontendDefault,
			wantCSSJS: true,
		},
		{
			name:            "valid player only",
			paths:           AppearancePaths{CustomPlayerPath: playerPath},
			wantMode:        FrontendCustomPlayer,
			wantCSSJS:       true,
			wantPlayerValid: true,
		},
		{
			name:           "valid frontend only",
			paths:          AppearancePaths{CustomFrontendDir: frontendDir},
			wantMode:       FrontendCustomFrontend,
			wantCSSJS:      false,
			wantFrontValid: true,
		},
		{
			name:           "valid frontend wins over valid player",
			paths:          AppearancePaths{CustomFrontendDir: frontendDir, CustomPlayerPath: playerPath},
			wantMode:       FrontendCustomFrontend,
			wantCSSJS:      false,
			wantFrontValid: true,
		},
		{
			name:            "invalid frontend falls back to valid player",
			paths:           AppearancePaths{CustomFrontendDir: filepath.Join(t.TempDir(), "missing"), CustomPlayerPath: playerPath},
			wantMode:        FrontendCustomPlayer,
			wantCSSJS:       true,
			wantPlayerValid: true,
			wantFrontError:  true,
		},
		{
			name:           "invalid frontend and no player falls back to default",
			paths:          AppearancePaths{CustomFrontendDir: filepath.Join(t.TempDir(), "missing")},
			wantMode:       FrontendDefault,
			wantCSSJS:      true,
			wantFrontError: true,
		},
		{
			name:            "missing player file",
			paths:           AppearancePaths{CustomPlayerPath: filepath.Join(t.TempDir(), "nope.js")},
			wantMode:        FrontendDefault,
			wantCSSJS:       true,
			wantPlayerError: true,
		},
		{
			name:            "relative player path rejected",
			paths:           AppearancePaths{CustomPlayerPath: "player.js"},
			wantMode:        FrontendDefault,
			wantCSSJS:       true,
			wantPlayerError: true,
		},
		{
			name:            "traversal player path rejected",
			paths:           AppearancePaths{CustomPlayerPath: "../../etc/passwd"},
			wantMode:        FrontendDefault,
			wantCSSJS:       true,
			wantPlayerError: true,
		},
		{
			name:            "player path that is a directory rejected",
			paths:           AppearancePaths{CustomPlayerPath: t.TempDir()},
			wantMode:        FrontendDefault,
			wantCSSJS:       true,
			wantPlayerError: true,
		},
		{
			name:           "frontend without index.html rejected",
			paths:          AppearancePaths{CustomFrontendDir: t.TempDir()},
			wantMode:       FrontendDefault,
			wantCSSJS:      true,
			wantFrontError: true,
		},
		{
			name:           "frontend path that is a file rejected",
			paths:          AppearancePaths{CustomFrontendDir: playerPath},
			wantMode:       FrontendDefault,
			wantCSSJS:      true,
			wantFrontError: true,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			state := ResolveAppearance(testCase.paths)
			if state.Mode != testCase.wantMode {
				t.Fatalf("mode = %q, want %q (state %+v)", state.Mode, testCase.wantMode, state)
			}
			if state.CustomCSSAndJSServed() != testCase.wantCSSJS {
				t.Errorf("CustomCSSAndJSServed() = %v, want %v", state.CustomCSSAndJSServed(), testCase.wantCSSJS)
			}
			if state.PlayerValid != testCase.wantPlayerValid {
				t.Errorf("PlayerValid = %v, want %v (error %q)", state.PlayerValid, testCase.wantPlayerValid, state.PlayerError)
			}
			if state.FrontendValid != testCase.wantFrontValid {
				t.Errorf("FrontendValid = %v, want %v (error %q)", state.FrontendValid, testCase.wantFrontValid, state.FrontendError)
			}
			if testCase.wantPlayerError && state.PlayerError == "" {
				t.Error("expected a player path validation error")
			}
			if testCase.wantFrontError && state.FrontendError == "" {
				t.Error("expected a frontend directory validation error")
			}
			if state.Mode == FrontendCustomPlayer && state.PlayerPath == "" {
				t.Error("active custom player mode has no resolved player path")
			}
			if state.Mode == FrontendCustomFrontend && state.FrontendDir == "" {
				t.Error("active custom frontend mode has no resolved directory")
			}
			if state.Mode == FrontendCustomFrontend && state.PlayerPath != "" {
				t.Error("custom frontend mode must ignore the custom player path")
			}
		})
	}
}

func TestCustomPlayerPathInsideDataDirectoryRejected(t *testing.T) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dataDirectory := filepath.Join(workingDirectory, "data")
	playerPath := filepath.Join(dataDirectory, "appearance-test-player.js")
	writeAppearanceFile(t, playerPath, "export default function mount() {}\n")
	t.Cleanup(func() { _ = os.RemoveAll(dataDirectory) })

	if _, err := ValidatePlayerPath(playerPath); err == nil {
		t.Fatal("expected a player path inside the private data directory to be rejected")
	}

	frontendDir := filepath.Join(dataDirectory, "appearance-test-frontend")
	writeAppearanceFile(t, filepath.Join(frontendDir, "index.html"), "<html></html>")
	if _, err := ValidateFrontendDir(frontendDir); err == nil {
		t.Fatal("expected a frontend directory inside the private data directory to be rejected")
	}
}

func TestCustomPlayerSymlinkEscapeRejected(t *testing.T) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dataDirectory := filepath.Join(workingDirectory, "data")
	target := filepath.Join(dataDirectory, "appearance-symlink-target.js")
	writeAppearanceFile(t, target, "export default function mount() {}\n")
	t.Cleanup(func() { _ = os.RemoveAll(dataDirectory) })

	link := filepath.Join(t.TempDir(), "player.js")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if _, err := ValidatePlayerPath(link); err == nil {
		t.Fatal("expected a symlink escaping into the private data directory to be rejected")
	}
}
