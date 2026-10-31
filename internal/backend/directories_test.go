package backend

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestDirectoryBrowsing(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"beta", "Alpha", "한글 공백"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "Alpha", "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "image.jpg")
	if err := os.WriteFile(file, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	request := func(values url.Values) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		listDirectories(response, httptest.NewRequest(http.MethodGet, "/api/directories?"+values.Encode(), nil))
		return response
	}
	page := func(values url.Values) directoryPage {
		t.Helper()
		response := request(values)
		if response.Code != 200 {
			t.Fatalf("%d: %s", response.Code, response.Body.String())
		}
		var result directoryPage
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := page(url.Values{"limit": {"2"}})
	canonical, err := realPath(root)
	if err != nil {
		t.Fatal(err)
	}
	if first.Path != canonical || first.Total != 3 || len(first.Entries) != 2 || first.Entries[0].Name != "Alpha" || first.Entries[1].Name != "beta" {
		t.Fatalf("unexpected first page: %+v", first)
	}
	if first.Home != root || len(first.Roots) == 0 || first.Parent != filepath.Dir(canonical) {
		t.Fatalf("navigation metadata missing: %+v", first)
	}
	last := page(url.Values{"path": {canonical}, "offset": {"2"}, "limit": {"2"}})
	if len(last.Entries) != 1 || last.Entries[0].Name != "한글 공백" || last.Entries[0].Path != filepath.Join(canonical, "한글 공백") {
		t.Fatalf("unexpected last page: %+v", last)
	}
	if got := page(url.Values{"offset": {"999999999"}}); len(got.Entries) != 0 {
		t.Fatal("offset beyond the end should return an empty page")
	}
	child := page(url.Values{"path": {filepath.Join(root, "Alpha", "nested")}})
	if child.Total != 0 || len(child.Entries) != 0 || child.Parent != filepath.Join(canonical, "Alpha") {
		t.Fatalf("empty directory not selectable: %+v", child)
	}
	for _, values := range []url.Values{{"offset": {"-1"}}, {"limit": {"0"}}, {"limit": {"501"}}, {"offset": {"invalid"}}, {"path": {file}}} {
		if got := request(values).Code; got != 400 {
			t.Fatalf("invalid request %v: %d", values, got)
		}
	}
	if got := request(url.Values{"path": {filepath.Join(root, "missing")}}).Code; got != 404 {
		t.Fatalf("missing directory: %d", got)
	}
	volumeRoot := filepath.VolumeName(canonical) + string(filepath.Separator)
	if got := page(url.Values{"path": {volumeRoot}}); got.Parent != "" {
		t.Fatalf("filesystem root should not have a parent: %+v", got)
	}
}

func TestDirectoryLinkNavigationIsNotRecursive(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(root, "loop")
	createDirectoryLink(t, link, root)
	response := httptest.NewRecorder()
	listDirectories(response, httptest.NewRequest(http.MethodGet, "/api/directories?"+url.Values{"path": {link}}.Encode(), nil))
	var page directoryPage
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	canonical, err := realPath(root)
	if err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || page.Path != canonical || len(page.Entries) != 1 || page.Entries[0].Name != "loop" {
		t.Fatalf("link should resolve exactly one level: %+v (%d)", page, response.Code)
	}
}
