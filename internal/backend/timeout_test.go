package backend

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func legacyTimeoutApp(t *testing.T, requestSeconds int) (*App, *fakeVectors) {
	t.Helper()
	f := newFakeVectors(t)
	f.blockImage.Store(true)
	settings := DefaultSettings()
	settings.Embedding.BaseURL = f.server.URL + "/v1"
	settings.Qdrant.URL = f.server.URL
	settings.Processing.RequestTimeoutSeconds = requestSeconds
	encoded, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	var legacy map[string]any
	if err := json.Unmarshal(encoded, &legacy); err != nil {
		t.Fatal(err)
	}
	legacy["processing"].(map[string]any)["job_timeout_seconds"] = 1
	root := t.TempDir()
	writeTestImage(t, filepath.Join(root, "photo.jpg"))
	dir := t.TempDir()
	store, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Seed both legacy application settings and an interrupted job before startup.
	ctx := context.Background()
	for _, record := range []struct {
		kind, id string
		body     any
	}{
		{"settings", "current", legacy},
		{"source", "source", Source{ID: "source", RootPath: root, Active: true}},
		{"job", "legacy-job", map[string]any{"id": "legacy-job", "source_id": "source", "status": "running", "settings": legacy}},
	} {
		if err := store.put(ctx, record.kind, record.id, record.body); err != nil {
			_ = store.db.Close()
			t.Fatal(err)
		}
	}
	if err := store.db.Close(); err != nil {
		t.Fatal(err)
	}
	app, err := New(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	return app, f
}

func TestLegacyScanHasNoOverallTimeoutAndCanBeCanceled(t *testing.T) {
	app, f := legacyTimeoutApp(t, 180)
	select {
	case <-f.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("resumed job did not reach image embedding")
	}
	// Stay in an individual request beyond the old one-second overall deadline.
	<-time.After(1500 * time.Millisecond)
	var job Job
	if err := app.store.get(context.Background(), "job", "legacy-job", &job); err != nil {
		t.Fatal(err)
	}
	if job.Status != "running" {
		t.Fatalf("legacy overall timeout interrupted the scan: %+v", job)
	}
	if err := app.CancelJob(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	if finished := awaitJob(t, app, job.ID); finished.Status != "canceled" || finished.Error != context.Canceled.Error() {
		t.Fatalf("unlimited scan must remain manually cancellable: %+v", finished)
	}
}

func TestScanStillBoundsIndividualRequests(t *testing.T) {
	app, _ := legacyTimeoutApp(t, 1)
	job := awaitJob(t, app, "legacy-job")
	if job.Status != "failed" || job.IssueCount != 1 {
		t.Fatalf("individual embedding timeout must fail the file without canceling the whole job: %+v", job)
	}
}

func TestIndividualTimeoutSettingsRemainRequired(t *testing.T) {
	for _, field := range []string{"request", "process"} {
		t.Run(field, func(t *testing.T) {
			settings := DefaultSettings()
			if field == "request" {
				settings.Processing.RequestTimeoutSeconds = 0
			} else {
				settings.Processing.ProcessTimeoutSeconds = 0
			}
			if _, err := resolve(settings); err == nil {
				t.Fatal("individual timeout cannot be disabled")
			}
		})
	}
}
