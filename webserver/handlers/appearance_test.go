package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCleanFrontendRequestPath(t *testing.T) {
	tests := []struct {
		urlPath string
		want    string
	}{
		{"/", ""},
		{"/index.html", "index.html"},
		{"/assets/app.js", "assets/app.js"},
		{"/embed/video/", "embed/video"},
		{"/../secret", "secret"},
		{"/a/../../b", "b"},
		{"\\windows\\path", ""},
		{"/bad\x00path", ""},
	}
	for _, testCase := range tests {
		if got := cleanFrontendRequestPath(testCase.urlPath); got != testCase.want {
			t.Errorf("cleanFrontendRequestPath(%q) = %q, want %q", testCase.urlPath, got, testCase.want)
		}
	}
}

func TestServeCustomFrontend(t *testing.T) {
	directory := t.TempDir()
	if err := os.MkdirAll(filepath.Join(directory, "assets"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "index.html"), []byte("<html>CUSTOM INDEX</html>"), 0o600); err != nil {
		t.Fatalf("write index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "assets", "app.js"), []byte("console.log('asset')"), 0o600); err != nil {
		t.Fatalf("write asset: %v", err)
	}

	handler := &Handlers{}
	serve := func(method, urlPath string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, "http://example.com"+urlPath, nil)
		response := httptest.NewRecorder()
		handler.serveCustomFrontend(response, request, directory)
		return response
	}

	index := serve(http.MethodGet, "/")
	if index.Code != http.StatusOK || index.Body.String() != "<html>CUSTOM INDEX</html>" {
		t.Fatalf("index response = %d %q", index.Code, index.Body.String())
	}

	asset := serve(http.MethodGet, "/assets/app.js")
	if asset.Code != http.StatusOK || asset.Body.String() != "console.log('asset')" {
		t.Fatalf("asset response = %d %q", asset.Code, asset.Body.String())
	}

	fallback := serve(http.MethodGet, "/client/side/route")
	if fallback.Code != http.StatusOK || fallback.Body.String() != "<html>CUSTOM INDEX</html>" {
		t.Fatalf("SPA fallback response = %d %q", fallback.Code, fallback.Body.String())
	}

	traversal := serve(http.MethodGet, "/../../etc/passwd")
	if traversal.Body.String() == "" || traversal.Code != http.StatusOK {
		t.Fatalf("traversal response = %d %q", traversal.Code, traversal.Body.String())
	}
	if traversal.Body.String() != "<html>CUSTOM INDEX</html>" {
		t.Fatalf("traversal escaped the root: %q", traversal.Body.String())
	}

	methodNotAllowed := serve(http.MethodPost, "/")
	if methodNotAllowed.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST response = %d, want 405", methodNotAllowed.Code)
	}
}
