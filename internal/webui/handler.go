// Package webui serves the frontend built by npm into the Go executable.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:assets
var embedded embed.FS

func Handler() http.Handler {
	assets, _ := fs.Sub(embedded, "assets")
	files := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if strings.HasPrefix(name, "api/") || strings.HasPrefix(name, ".") {
			http.NotFound(w, r)
			return
		}
		if _, err := fs.Stat(assets, name); err != nil {
			if path.Ext(name) != "" {
				http.NotFound(w, r)
				return
			}
			name = "index.html"
		}
		if _, err := fs.Stat(assets, "index.html"); err != nil {
			http.Error(w, "Web UI is not built. Run npm ci --prefix web and npm run build --prefix web, then rebuild Go.", http.StatusServiceUnavailable)
			return
		}
		request := r.Clone(r.Context())
		request.URL.Path = "/" + name
		if name == "index.html" {
			request.URL.Path = "/"
		}
		files.ServeHTTP(w, request)
	})
}
