package backend

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"math"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func apiError(w http.ResponseWriter, status int, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		status = 404
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
func decodeBody(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return errors.New("expected one JSON value")
	}
	return nil
}

// Handler exposes a loopback-only, single-user API with browser cross-origin protection.
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /api/settings", a.getSettings)
	mux.HandleFunc("PUT /api/settings", func(w http.ResponseWriter, r *http.Request) {
		var s Settings
		if err := decodeBody(w, r, &s); err != nil {
			apiError(w, 400, err)
			return
		}
		if err := a.SaveSettings(r.Context(), s); err != nil {
			apiError(w, 400, err)
			return
		}
		a.getSettings(w, r)
	})
	mux.HandleFunc("GET /api/models", func(w http.ResponseWriter, r *http.Request) {
		cfg, err := resolve(a.Settings())
		if err != nil {
			apiError(w, 400, err)
			return
		}
		models, err := newRemote(cfg).models(r.Context())
		if err != nil {
			apiError(w, 502, err)
			return
		}
		writeJSON(w, 200, models)
	})
	mux.HandleFunc("POST /api/connections/test", a.testConnections)
	mux.HandleFunc("GET /api/sources", func(w http.ResponseWriter, r *http.Request) {
		sources, err := a.Sources(r.Context())
		if err != nil {
			apiError(w, 500, err)
			return
		}
		writeJSON(w, 200, sources)
	})
	mux.HandleFunc("POST /api/sources", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			RootPath string `json:"root_path"`
		}
		if err := decodeBody(w, r, &body); err != nil {
			apiError(w, 400, err)
			return
		}
		source, err := a.AddSource(r.Context(), body.RootPath)
		if err != nil {
			apiError(w, 400, err)
			return
		}
		writeJSON(w, 201, source)
	})
	mux.HandleFunc("DELETE /api/sources/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := a.RemoveSource(r.Context(), r.PathValue("id")); err != nil {
			apiError(w, 400, err)
			return
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /api/sources/{id}/scan", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Force bool `json:"force"`
		}
		if r.ContentLength != 0 {
			if err := decodeBody(w, r, &body); err != nil {
				apiError(w, 400, err)
				return
			}
		}
		job, err := a.StartJob(r.Context(), r.PathValue("id"), body.Force)
		if err != nil {
			apiError(w, 409, err)
			return
		}
		writeJSON(w, 202, job)
	})
	mux.HandleFunc("GET /api/jobs", func(w http.ResponseWriter, r *http.Request) {
		jobs, err := listRecords[Job](r.Context(), a.store, "job")
		if err != nil {
			apiError(w, 500, err)
			return
		}
		writeJSON(w, 200, jobs)
	})
	mux.HandleFunc("GET /api/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		var job Job
		if err := a.store.get(r.Context(), "job", r.PathValue("id"), &job); err != nil {
			apiError(w, 404, err)
			return
		}
		writeJSON(w, 200, job)
	})
	mux.HandleFunc("POST /api/jobs/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		if err := a.CancelJob(r.Context(), r.PathValue("id")); err != nil {
			apiError(w, 400, err)
			return
		}
		w.WriteHeader(202)
	})
	mux.HandleFunc("POST /api/jobs/{id}/retry", func(w http.ResponseWriter, r *http.Request) {
		var prior Job
		if err := a.store.get(r.Context(), "job", r.PathValue("id"), &prior); err != nil {
			apiError(w, 404, err)
			return
		}
		job, err := a.StartJob(r.Context(), prior.SourceID, prior.Force)
		if err != nil {
			apiError(w, 409, err)
			return
		}
		writeJSON(w, 202, job)
	})
	mux.HandleFunc("POST /api/search", func(w http.ResponseWriter, r *http.Request) {
		var query SearchRequest
		if err := decodeBody(w, r, &query); err != nil {
			apiError(w, 400, err)
			return
		}
		results, err := a.Search(r.Context(), query)
		if err != nil {
			apiError(w, 502, err)
			return
		}
		writeJSON(w, 200, map[string]any{"results": results})
	})
	mux.HandleFunc("GET /api/assets", a.listAssets)
	mux.HandleFunc("GET /api/assets/{id}", func(w http.ResponseWriter, r *http.Request) {
		asset, _, err := a.asset(r.Context(), r.PathValue("id"))
		if err != nil {
			apiError(w, 404, err)
			return
		}
		writeJSON(w, 200, asset)
	})
	mux.HandleFunc("GET /api/assets/{id}/thumbnail", a.thumbnail)
	mux.HandleFunc("GET /api/assets/{id}/content", a.content)
	mux.HandleFunc("GET /api/assets/{id}/download", a.content)
	mux.HandleFunc("POST /api/assets/{id}/export", a.export)
	mux.HandleFunc("GET /api/cleanup", func(w http.ResponseWriter, r *http.Request) {
		tasks, err := listRecords[cleanupTask](r.Context(), a.store, "cleanup")
		if err != nil {
			apiError(w, 500, err)
			return
		}
		writeJSON(w, 200, tasks)
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"name": "vgalary", "status": "backend ready", "settings": "/api/settings", "documentation": "docs/api.md"})
	})
	protected := http.NewCrossOriginProtection().Handler(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		ip := net.ParseIP(host)
		if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
			apiError(w, 403, errors.New("loopback Host required"))
			return
		}
		protected.ServeHTTP(w, r)
	})
}
func (a *App) getSettings(w http.ResponseWriter, r *http.Request) {
	s := a.Settings()
	sources, err := a.Sources(r.Context())
	if err != nil {
		apiError(w, 500, err)
		return
	}
	out := map[string]any{"settings": s, "sources": sources}
	var activeProfile string
	if err := a.store.get(r.Context(), "meta", "active_profile", &activeProfile); err == nil {
		out["active_profile"] = activeProfile
	}
	cfg, err := resolve(s)
	if err != nil {
		out["configuration_error"] = err.Error()
	} else {
		out["effective"] = map[string]any{"embedding_url": cfg.EmbeddingURL, "model": cfg.Model, "qdrant_url": cfg.QdrantURL, "embedding_auth_configured": cfg.EmbeddingKey != "", "qdrant_auth_configured": cfg.QdrantKey != ""}
	}
	writeJSON(w, 200, out)
}
func (a *App) testConnections(w http.ResponseWriter, r *http.Request) {
	cfg, err := resolve(a.Settings())
	if err != nil {
		apiError(w, 400, err)
		return
	}
	remote := newRemote(cfg)
	if _, err := remote.models(r.Context()); err != nil {
		apiError(w, 502, err)
		return
	}
	text, err := remote.embedText(r.Context(), "a red square")
	if err != nil {
		apiError(w, 502, err)
		return
	}
	vectors := [][]float32{}
	for _, c := range []color.RGBA{{R: 255, A: 255}, {B: 255, A: 255}} {
		img := image.NewRGBA(image.Rect(0, 0, 64, 64))
		for y := 0; y < 64; y++ {
			for x := 0; x < 64; x++ {
				img.SetRGBA(x, y, c)
			}
		}
		var b bytes.Buffer
		if err := jpeg.Encode(&b, img, nil); err != nil {
			apiError(w, 500, err)
			return
		}
		vector, err := remote.embedImage(r.Context(), b.Bytes())
		if err != nil {
			apiError(w, 502, err)
			return
		}
		if len(vector) != len(text) {
			apiError(w, 502, errors.New("text/image dimensions differ"))
			return
		}
		vectors = append(vectors, vector)
	}
	similarity := cosine(vectors[0], vectors[1])
	if similarity > 0.999999 {
		apiError(w, 502, errors.New("different test images returned indistinguishable vectors"))
		return
	}
	if err := remote.qdrant(r.Context(), "GET", "/collections", nil, new(any)); err != nil {
		apiError(w, 502, err)
		return
	}
	writeJSON(w, 200, map[string]any{"model": cfg.Model, "dimensions": len(text), "image_similarity": similarity, "red_query_red_image_similarity": cosine(text, vectors[0]), "red_query_blue_image_similarity": cosine(text, vectors[1]), "qdrant": "ok"})
}
func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i, v := range a {
		dot += float64(v) * float64(b[i])
		na += float64(v) * float64(v)
		nb += float64(b[i]) * float64(b[i])
	}
	return dot / math.Sqrt(na*nb)
}
func (a *App) listAssets(w http.ResponseWriter, r *http.Request) {
	profileID := r.URL.Query().Get("profile_id")
	if profileID == "" {
		if err := a.store.get(r.Context(), "meta", "active_profile", &profileID); err != nil && !errors.Is(err, sql.ErrNoRows) {
			apiError(w, 500, err)
			return
		}
	}
	assets, err := listRecords[Asset](r.Context(), a.store, "asset")
	if err != nil {
		apiError(w, 500, err)
		return
	}
	sources, err := a.Sources(r.Context())
	if err != nil {
		apiError(w, 500, err)
		return
	}
	active := map[string]bool{}
	for _, s := range sources {
		active[s.ID] = true
	}
	result := []Asset{}
	for _, asset := range assets {
		if active[asset.SourceID] && (profileID == "" || asset.ProfileID == profileID) && (r.URL.Query().Get("source_id") == "" || r.URL.Query().Get("source_id") == asset.SourceID) {
			result = append(result, asset)
		}
	}
	writeJSON(w, 200, result)
}
func (a *App) thumbnail(w http.ResponseWriter, r *http.Request) {
	asset, source, err := a.asset(r.Context(), r.PathValue("id"))
	if err != nil {
		apiError(w, 404, err)
		return
	}
	if err := a.checkOriginal(asset, source); err != nil {
		apiError(w, 409, err)
		return
	}
	pointID := r.URL.Query().Get("point_id")
	if pointID == "" && len(asset.Points) > 0 {
		pointID = asset.Points[0].ID
	}
	for _, point := range asset.Points {
		if point.ID == pointID {
			filename := filepath.Join(a.dir, "thumbnails", point.ID+".jpg")
			if _, err := os.Stat(filename); err != nil {
				apiError(w, 404, errors.New("preview unavailable"))
				return
			}
			http.ServeFile(w, r, filename)
			return
		}
	}
	apiError(w, 404, errors.New("point not found"))
}
func (a *App) openContent(ctx context.Context, id string) (Asset, Source, string, func(), error) {
	asset, source, err := a.asset(ctx, id)
	if err != nil {
		return asset, source, "", func() {}, err
	}
	if err := a.checkOriginal(asset, source); err != nil {
		return asset, source, "", func() {}, err
	}
	dir, err := os.MkdirTemp(filepath.Join(a.dir, "tmp"), "download-")
	if err != nil {
		return asset, source, "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	settings := a.Settings()
	processCtx, cancel := context.WithTimeout(ctx, time.Duration(settings.Processing.ProcessTimeoutSeconds)*time.Second)
	defer cancel()
	name, err := materialize(processCtx, source, asset.Location, dir, settings.Processing)
	if err == nil {
		err = a.checkOriginal(asset, source)
	}
	if err != nil {
		cleanup()
		return asset, source, "", func() {}, err
	}
	return asset, source, name, cleanup, nil
}
func (a *App) content(w http.ResponseWriter, r *http.Request) {
	asset, _, err := a.asset(r.Context(), r.PathValue("id"))
	if err != nil {
		apiError(w, 404, err)
		return
	}
	download := strings.HasSuffix(r.URL.Path, "/download") || r.URL.Query().Get("download") == "1"
	if len(asset.Location.ArchiveEntries) > 0 && !download {
		apiError(w, 409, errors.New("archive preview unavailable; use explicit download"))
		return
	}
	asset, _, filename, cleanup, err := a.openContent(r.Context(), asset.ID)
	if err != nil {
		apiError(w, 409, err)
		return
	}
	defer cleanup()
	f, err := os.Open(filename)
	if err != nil {
		apiError(w, 404, err)
		return
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		apiError(w, 500, err)
		return
	}
	name := assetFilename(asset)
	if download {
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	}
	http.ServeContent(w, r, name, info.ModTime(), f)
}
func assetFilename(asset Asset) string {
	if n := len(asset.Location.ArchiveEntries); n > 0 {
		return filepath.Base(strings.ReplaceAll(asset.Location.ArchiveEntries[n-1].Name, `\`, "/"))
	}
	return filepath.Base(asset.Location.RelativePath)
}
func (a *App) export(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Directory string `json:"directory"`
	}
	if err := decodeBody(w, r, &body); err != nil {
		apiError(w, 400, err)
		return
	}
	if body.Directory == "" {
		apiError(w, 400, errors.New("directory is required"))
		return
	}
	destination, err := filepath.Abs(body.Directory)
	if err != nil {
		apiError(w, 400, err)
		return
	}
	// Require an existing canonical directory; symlinks cannot bypass overlap checks.
	destination, err = realPath(destination)
	if err != nil {
		apiError(w, 400, err)
		return
	}
	if inside(a.dir, destination) || inside(destination, a.dir) {
		apiError(w, 400, errors.New("export and app data must not overlap"))
		return
	}
	sources, err := a.Sources(r.Context())
	if err != nil {
		apiError(w, 500, err)
		return
	}
	for _, source := range sources {
		if inside(source.RootPath, destination) || inside(destination, source.RootPath) {
			apiError(w, 400, errors.New("export and sources must not overlap"))
			return
		}
	}
	asset, _, filename, cleanup, err := a.openContent(r.Context(), r.PathValue("id"))
	if err != nil {
		apiError(w, 409, err)
		return
	}
	defer cleanup()
	input, err := os.Open(filename)
	if err != nil {
		apiError(w, 500, err)
		return
	}
	defer func() { _ = input.Close() }()
	name := assetFilename(asset)
	extension := filepath.Ext(name)
	target := filepath.Join(destination, strings.TrimSuffix(name, extension)+"-"+uuid.NewString()+extension)
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		apiError(w, 500, err)
		return
	}
	_, copyErr := io.Copy(output, contextReader{r.Context(), input})
	closeErr := output.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(target)
		apiError(w, 500, fmt.Errorf("export did not complete"))
		return
	}
	writeJSON(w, 201, map[string]string{"path": target})
}
