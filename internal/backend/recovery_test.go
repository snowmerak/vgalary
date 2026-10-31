package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestEmbeddingRetryPreservesBearerHeader(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("retry corrupted authorization header")
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(503)
			return
		}
		writeJSON(w, 200, map[string]any{"data": []any{map[string]any{"index": 0, "embedding": []float32{1, 2}}}})
	}))
	defer server.Close()
	cfg, _ := resolve(DefaultSettings())
	cfg.EmbeddingURL = server.URL
	cfg.EmbeddingKey = "test-key"
	vector, err := newRemote(cfg).embedText(context.Background(), "query")
	if err != nil || len(vector) != 2 || calls.Load() != 2 {
		t.Fatalf("retry failed: %v", err)
	}
}

type fakeVectors struct {
	server       *httptest.Server
	mu           sync.Mutex
	points       map[string][]float32
	blockImage   atomic.Bool
	imageCalls   atomic.Int32
	blockAfter   atomic.Int32
	entered      chan struct{}
	unavailable  atomic.Bool
	expectedAuth atomic.Value
}

func newFakeVectors(t *testing.T) *fakeVectors {
	t.Helper()
	f := &fakeVectors{points: map[string][]float32{}, entered: make(chan struct{}, 1)}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/embeddings" {
			if expected := f.expectedAuth.Load(); expected != nil && r.Header.Get("Authorization") != expected.(string) {
				w.WriteHeader(401)
				return
			}
			var body struct {
				Input any `json:"input"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			_, isText := body.Input.(string)
			imageCall := int32(0)
			if !isText {
				imageCall = f.imageCalls.Add(1)
			}
			if !isText && (f.blockImage.Load() || (f.blockAfter.Load() > 0 && imageCall > f.blockAfter.Load())) {
				select {
				case f.entered <- struct{}{}:
				default:
				}
				<-r.Context().Done()
				return
			}
			writeJSON(w, 200, map[string]any{"data": []any{map[string]any{"index": 0, "embedding": []float32{1, 2, 3}}}})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/points/query") {
			f.mu.Lock()
			points := []vectorHit{}
			for id := range f.points {
				points = append(points, vectorHit{ID: id, Score: 0.8})
			}
			f.mu.Unlock()
			writeJSON(w, 200, map[string]any{"result": map[string]any{"points": points}})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/points/delete") {
			if f.unavailable.Load() {
				w.WriteHeader(503)
				return
			}
			var body struct {
				Points []string `json:"points"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			for _, id := range body.Points {
				delete(f.points, id)
			}
			f.mu.Unlock()
			writeJSON(w, 200, map[string]any{"result": map[string]any{"status": "completed"}})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/points") && r.Method == "PUT" {
			var body struct {
				Points []struct {
					ID     string    `json:"id"`
					Vector []float32 `json:"vector"`
				} `json:"points"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			for _, p := range body.Points {
				f.points[p.ID] = p.Vector
			}
			f.mu.Unlock()
			writeJSON(w, 200, map[string]any{"result": map[string]any{"status": "completed"}})
			return
		}
		if _, id, ok := strings.Cut(r.URL.Path, "/points/"); ok {
			f.mu.Lock()
			vector, found := f.points[id]
			f.mu.Unlock()
			if !found {
				w.WriteHeader(404)
				return
			}
			writeJSON(w, 200, map[string]any{"result": map[string]any{"vector": vector}})
			return
		}
		writeJSON(w, 200, map[string]any{"result": map[string]any{"config": map[string]any{"params": map[string]any{"vectors": map[string]any{"size": 3, "distance": "Cosine"}}}}})
	}))
	t.Cleanup(f.server.Close)
	return f
}
func fakeApp(t *testing.T, f *fakeVectors) *App {
	t.Helper()
	app, err := New(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := app.Close(); err != nil {
			t.Error(err)
		}
	})
	s := app.Settings()
	s.Embedding.BaseURL = f.server.URL + "/v1"
	s.Qdrant.URL = f.server.URL
	if err := app.SaveSettings(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	return app
}
func writeTestImage(t *testing.T, name string) {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 16, 16)), nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, b.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
}
func awaitJob(t *testing.T, app *App, id string) Job {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-deadline.C:
			t.Fatal("job did not terminate")
		case <-tick.C:
		}
		var job Job
		if err := app.store.get(context.Background(), "job", id, &job); err != nil {
			t.Fatal(err)
		}
		if job.Status != "queued" && job.Status != "running" {
			return job
		}
	}
}
func scanTestSource(t *testing.T, app *App, source Source) Job {
	t.Helper()
	job, err := app.StartJob(context.Background(), source.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	return awaitJob(t, app, job.ID)
}

func TestIncompleteScanRetainsUnseenAssets(t *testing.T) {
	f := newFakeVectors(t)
	app := fakeApp(t, f)
	root := t.TempDir()
	name := filepath.Join(root, "photo.jpg")
	writeTestImage(t, name)
	source, err := app.AddSource(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if job := scanTestSource(t, app, source); job.Status != "succeeded" {
		t.Fatal(job.Error)
	}
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	invalid := filepath.Join(root, "broken.zip")
	if err := os.WriteFile(invalid, makeZIP(t, "../escape.jpg", []byte("bad")), 0600); err != nil {
		t.Fatal(err)
	}
	if job := scanTestSource(t, app, source); job.Status != "failed" {
		t.Fatal("incomplete scan reported success")
	}
	assets, err := listRecords[Asset](context.Background(), app.store, "asset")
	if err != nil || len(assets) != 1 {
		t.Fatal("incomplete scan deleted unseen asset")
	}
	if err := os.Remove(invalid); err != nil {
		t.Fatal(err)
	}
	if job := scanTestSource(t, app, source); job.Status != "succeeded" {
		t.Fatal(job.Error)
	}
	assets, err = listRecords[Asset](context.Background(), app.store, "asset")
	if err != nil || len(assets) != 0 {
		t.Fatal("completed scan did not remove deleted asset")
	}
}

func TestSourceRemovalDuringIndexing(t *testing.T) {
	f := newFakeVectors(t)
	f.blockImage.Store(true)
	app := fakeApp(t, f)
	root := t.TempDir()
	writeTestImage(t, filepath.Join(root, "photo.jpg"))
	source, err := app.AddSource(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	job, err := app.StartJob(context.Background(), source.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("image request did not start")
	}
	if err := app.RemoveSource(context.Background(), source.ID); err != nil {
		t.Fatal(err)
	}
	finished := awaitJob(t, app, job.ID)
	if finished.Status != "canceled" {
		t.Fatalf("removed source job status: %s", finished.Status)
	}
	assets, err := listRecords[Asset](context.Background(), app.store, "asset")
	if err != nil || len(assets) != 0 {
		t.Fatal("removed source resurrected an asset")
	}
}

func TestRemovedSourceHiddenWhileQdrantUnavailable(t *testing.T) {
	f := newFakeVectors(t)
	app := fakeApp(t, f)
	root := t.TempDir()
	writeTestImage(t, filepath.Join(root, "photo.jpg"))
	source, err := app.AddSource(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if job := scanTestSource(t, app, source); job.Status != "succeeded" {
		t.Fatal(job.Error)
	}
	f.unavailable.Store(true)
	if err := app.RemoveSource(context.Background(), source.ID); err != nil {
		t.Fatal(err)
	}
	results, err := app.Search(context.Background(), SearchRequest{Query: "photo"})
	if err != nil || len(results) != 0 {
		t.Fatal("removed source reappeared while remote deletion was unavailable")
	}
	tasks, err := listRecords[cleanupTask](context.Background(), app.store, "cleanup")
	if err != nil || len(tasks) == 0 {
		t.Fatal("remote deletion intent was lost")
	}
}

func TestNewProfileWaitsForAllSources(t *testing.T) {
	f := newFakeVectors(t)
	app := fakeApp(t, f)
	sources := []Source{}
	for i := 0; i < 2; i++ {
		root := t.TempDir()
		writeTestImage(t, filepath.Join(root, "photo.jpg"))
		s, err := app.AddSource(context.Background(), root)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, s)
		if job := scanTestSource(t, app, s); job.Status != "succeeded" {
			t.Fatal(job.Error)
		}
	}
	var before string
	if err := app.store.get(context.Background(), "meta", "active_profile", &before); err != nil {
		t.Fatal(err)
	}
	settings := app.Settings()
	settings.Embedding.ModelRevision = "new-weights"
	if err := app.SaveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	if job := scanTestSource(t, app, sources[0]); job.Status != "succeeded" {
		t.Fatal(job.Error)
	}
	var current string
	if err := app.store.get(context.Background(), "meta", "active_profile", &current); err != nil {
		t.Fatal(err)
	}
	if current != before {
		t.Fatal("new profile activated before all roots were indexed")
	}
	results := searchTestAPI(t, app, SearchRequest{Query: "photo"})
	if len(results) != 2 {
		t.Fatal("existing search was interrupted by a pending profile")
	}
	for _, result := range results {
		if result.Asset.ProfileID != before {
			t.Fatal("search mixed vector spaces before profile switch")
		}
	}
	if job := scanTestSource(t, app, sources[1]); job.Status != "succeeded" {
		t.Fatal(job.Error)
	}
	if err := app.store.get(context.Background(), "meta", "active_profile", &current); err != nil {
		t.Fatal(err)
	}
	if current == before {
		t.Fatal("new profile never activated")
	}
}

func TestSearchUsesUpdatedCredentialReference(t *testing.T) {
	f := newFakeVectors(t)
	app := fakeApp(t, f)
	root := t.TempDir()
	writeTestImage(t, filepath.Join(root, "photo.jpg"))
	source, err := app.AddSource(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if job := scanTestSource(t, app, source); job.Status != "succeeded" {
		t.Fatal(job.Error)
	}
	t.Setenv("VGALARY_TEST_NEW_API_KEY", "new-key")
	f.expectedAuth.Store("Bearer new-key")
	settings := app.Settings()
	settings.Embedding.APIKeyEnv = "VGALARY_TEST_NEW_API_KEY"
	if err := app.SaveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Search(context.Background(), SearchRequest{Query: "photo"}); err != nil {
		t.Fatalf("search did not consume changed credential setting: %v", err)
	}
}
