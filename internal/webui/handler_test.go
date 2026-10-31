package webui

import (
	"io/fs"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmbeddedEntryHasNoRedirectLoop(t *testing.T) {
	response := httptest.NewRecorder()
	Handler().ServeHTTP(response, httptest.NewRequest("GET", "http://localhost/", nil))
	if _, err := fs.Stat(embedded, "assets/index.html"); err != nil {
		if response.Code != 503 {
			t.Fatal("unbuilt UI should explain its build prerequisite")
		}
		return
	}
	if response.Code != 200 || !strings.Contains(response.Body.String(), `id="root"`) {
		t.Fatalf("built app entry failed: %d %s", response.Code, response.Body.String())
	}
	for _, path := range []string{"/api/not-a-static-file", "/.gitkeep", "/assets/missing.js"} {
		response := httptest.NewRecorder()
		Handler().ServeHTTP(response, httptest.NewRequest("GET", "http://localhost"+path, nil))
		if response.Code != 404 {
			t.Fatalf("unexpected static fallback for %s", path)
		}
	}
}
