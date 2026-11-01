package backend

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func searchTestAPI(t *testing.T, app *App, request SearchRequest) []SearchResult {
	t.Helper()
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r := httptest.NewRequest(http.MethodPost, "http://localhost/api/search", bytes.NewReader(body)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("search: %d %s", w.Code, w.Body.String())
	}
	var response struct {
		Results []SearchResult `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Results == nil {
		t.Fatal("search must return a JSON array even when empty")
	}
	return response.Results
}

func requireNoActiveProfile(t *testing.T, app *App) {
	t.Helper()
	var profile string
	err := app.store.get(context.Background(), "meta", "active_profile", &profile)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("test must exercise an unactivated index: %q, %v", profile, err)
	}
}

func TestSearchDuringFirstScan(t *testing.T) {
	for _, committed := range []int32{0, 1} {
		name := "before_first_commit"
		if committed == 1 {
			name = "after_first_commit"
		}
		t.Run(name, func(t *testing.T) {
			f := newFakeVectors(t)
			if committed == 0 {
				f.blockImage.Store(true)
			} else {
				f.blockAfter.Store(committed)
			}
			app := fakeApp(t, f)
			settings := app.Settings()
			settings.Processing.EmbeddingBatchSize = 1
			if err := app.SaveSettings(context.Background(), settings); err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			writeTestImage(t, filepath.Join(root, "a.jpg"))
			// Distinct pixels prevent the second file from reusing the first vector.
			white := image.NewGray(image.Rect(0, 0, 16, 16))
			for i := range white.Pix {
				white.Pix[i] = 255
			}
			var b bytes.Buffer
			if err := jpeg.Encode(&b, white, nil); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "b.jpg"), b.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
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
				t.Fatal("index did not reach blocked image request")
			}
			requireNoActiveProfile(t, app)
			results := searchTestAPI(t, app, SearchRequest{Query: "white cat", Limit: 10})
			if len(results) != int(committed) {
				t.Fatalf("only committed files should be searchable: %+v", results)
			}
			if committed == 1 && (results[0].Path != filepath.Join(root, "a.jpg") || results[0].Asset.Location.RelativePath != "a.jpg") {
				t.Fatalf("stored location missing from search result: %+v", results[0])
			}
			var running Job
			if err := app.store.get(context.Background(), "job", job.ID, &running); err != nil || running.Status != "running" {
				t.Fatalf("search must finish while indexing is still running: %+v %v", running, err)
			}
			if err := app.CancelJob(context.Background(), job.ID); err != nil {
				t.Fatal(err)
			}
			if finished := awaitJob(t, app, job.ID); finished.Status != "canceled" {
				t.Fatal(finished.Status)
			}
			requireNoActiveProfile(t, app)
			if got := searchTestAPI(t, app, SearchRequest{Query: "cat"}); len(got) != int(committed) {
				t.Fatal("cancellation hid committed files")
			}
		})
	}
}

func TestSearchIncompleteFirstScanAfterRestart(t *testing.T) {
	f := newFakeVectors(t)
	logger, logs := testLogger(t, "debug")
	app := loggedApp(t, f, logger)
	root := t.TempDir()
	writeTestImage(t, filepath.Join(root, "photo.jpg"))
	if err := os.WriteFile(filepath.Join(root, "broken.zip"), makeZIP(t, "../escape.jpg", []byte("fixture")), 0600); err != nil {
		t.Fatal(err)
	}
	source, err := app.AddSource(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if job := scanTestSource(t, app, source); job.Status != "failed" || job.Indexed != 1 {
		t.Fatalf("expected a partial first scan: %+v", job)
	}
	requireNoActiveProfile(t, app)
	if got := searchTestAPI(t, app, SearchRequest{Query: "photo"}); len(got) != 1 {
		t.Fatal("incomplete scan hid committed file")
	}
	finished := logs.wait(t, "http.finished", "")
	dependencies := 0
	for _, record := range logs.records(t) {
		if record["event"] == "dependency.finished" && record["request_id"] == finished["request_id"] {
			dependencies++
			if record["profile_id"] == nil || record["profile_id"] == "" {
				t.Fatal("search dependency log omitted the unactivated profile")
			}
		}
	}
	if dependencies != 2 {
		t.Fatalf("expected correlated embedding and Qdrant logs, got %d", dependencies)
	}
	dir := app.dir
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	requireNoActiveProfile(t, reopened)
	if got := searchTestAPI(t, reopened, SearchRequest{Query: "photo"}); len(got) != 1 {
		t.Fatal("restart hid the unactivated profile")
	}
}

func TestSearchEmptyCatalogNeedsNoEmbedding(t *testing.T) {
	f := newFakeVectors(t)
	// Any accidental embedding request fails authentication.
	f.expectedAuth.Store("Bearer unused")
	app := fakeApp(t, f)
	if got := searchTestAPI(t, app, SearchRequest{Query: "photo"}); len(got) != 0 {
		t.Fatal("new installation must return an empty result")
	}
}

func TestSearchAvailableProfileSelection(t *testing.T) {
	for _, tc := range []struct {
		name, active, source, kind, want string
		settingsChanged                  bool
	}{
		{name: "prefer_current_settings", want: "current"},
		{name: "keep_active_profile", active: "old", want: "old"},
		{name: "unavailable_active_profile", active: "missing", want: "current"},
		{name: "filter_selects_other_profile", active: "old", source: "new-source", want: "current"},
		{name: "available_stored_settings", source: "old-source", want: "old"},
		{name: "latest_available_profile", settingsChanged: true, want: "old"},
		{name: "no_matching_kind", kind: "video"},
		{name: "removed_source", source: "removed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeVectors(t)
			app := fakeApp(t, f)
			ctx := context.Background()
			settings := app.Settings()
			cfg, err := resolve(settings)
			if err != nil {
				t.Fatal(err)
			}
			currentID := cfg.profileID() + "_" + digest([]byte(cfg.QdrantURL + "/" + cfg.Qdrant.CollectionPrefix))[:8]
			if tc.active != "" {
				if err := app.store.put(ctx, "meta", "active_profile", tc.active); err != nil {
					t.Fatal(err)
				}
			}
			for _, sourceID := range []string{"old-source", "new-source", "removed"} {
				source := Source{ID: sourceID, RootPath: t.TempDir(), Active: sourceID != "removed"}
				if err := app.store.put(ctx, "source", source.ID, source); err != nil {
					t.Fatal(err)
				}
			}
			for _, id := range []string{"old", currentID} {
				stored := settings
				if id == "old" {
					stored.Embedding.ModelRevision = "old-weights"
				}
				profile := Profile{ID: id, Collection: "test_" + id, Dimensions: 3, Model: cfg.Model, Settings: stored}
				if err := app.store.put(ctx, "profile", id, profile); err != nil {
					t.Fatal(err)
				}
			}
			for _, asset := range []Asset{
				{ID: "old", SourceID: "old-source", ProfileID: "old", UpdatedAt: time.Now()},
				{ID: "current", SourceID: "new-source", ProfileID: currentID, UpdatedAt: time.Now().Add(-time.Hour)},
				{ID: "removed", SourceID: "removed", ProfileID: "missing", UpdatedAt: time.Now().Add(time.Hour)},
				{ID: "orphan", SourceID: "missing", ProfileID: "missing", UpdatedAt: time.Now().Add(time.Hour)},
				{ID: "pending", SourceID: "new-source", ProfileID: "missing", UpdatedAt: time.Now().Add(time.Hour)},
			} {
				asset.MediaType = "image"
				if asset.ID != "pending" {
					asset.Points = []PointRef{{ID: asset.ID}}
				}
				if err := app.store.put(ctx, "asset", asset.ID, asset); err != nil {
					t.Fatal(err)
				}
				f.mu.Lock()
				f.points[asset.ID] = []float32{1, 2, 3}
				f.mu.Unlock()
			}
			if tc.settingsChanged {
				settings.Embedding.ModelRevision = "not-yet-indexed"
				if err := app.SaveSettings(ctx, settings); err != nil {
					t.Fatal(err)
				}
			}
			results := searchTestAPI(t, app, SearchRequest{Query: "photo", SourceID: tc.source, MediaType: tc.kind})
			if tc.want == "" {
				if len(results) != 0 {
					t.Fatalf("ineligible assets appeared in search: %+v", results)
				}
			} else if len(results) != 1 || results[0].Asset.ID != tc.want {
				t.Fatalf("wrong profile selected: %+v", results)
			}
			if tc.active == "" {
				requireNoActiveProfile(t, app)
			}
		})
	}
}
