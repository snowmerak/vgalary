package backend

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestBatchSettingsValidationAndLegacyRecords(t *testing.T) {
	base := DefaultSettings()
	if base.Processing.EmbeddingBatchSize != 100 {
		t.Fatal("default batch size must be 100")
	}
	baseResolved, _ := resolve(base)
	for _, size := range []int{-1, 0, 1, 100, 1000, 1001} {
		s := base
		s.Processing.EmbeddingBatchSize = size
		cfg, err := resolve(s)
		if (err == nil) != (size >= 1 && size <= 1000) {
			t.Fatalf("batch size %d: %v", size, err)
		}
		if err == nil && cfg.profileID() != baseResolved.profileID() {
			t.Fatal("batch size changed vector profile")
		}
	}
	raw, _ := json.Marshal(base)
	var old map[string]any
	if err := json.Unmarshal(raw, &old); err != nil {
		t.Fatal(err)
	}
	delete(old["processing"].(map[string]any), "embedding_batch_size")
	for _, kind := range []string{"settings", "profile", "job", "cleanup"} {
		var data []byte
		if kind == "settings" {
			data, _ = json.Marshal(old)
		} else {
			data, _ = json.Marshal(map[string]any{"settings": old})
		}
		var got Settings
		switch kind {
		case "settings":
			if err := decodeRecord(data, &got); err != nil {
				t.Fatal(err)
			}
		case "profile":
			var profile Profile
			if err := decodeRecord(data, &profile); err != nil {
				t.Fatal(err)
			}
			got = profile.Settings
		case "job":
			var job Job
			if err := decodeRecord(data, &job); err != nil {
				t.Fatal(err)
			}
			got = job.Settings
		case "cleanup":
			var task cleanupTask
			if err := decodeRecord(data, &task); err != nil {
				t.Fatal(err)
			}
			got = task.Settings
		}
		if got.Processing.EmbeddingBatchSize != 100 {
			t.Fatalf("legacy %s has no default", kind)
		}
		if _, err := resolve(got); err != nil {
			t.Fatalf("legacy %s unusable: %v", kind, err)
		}
	}
	f := newFakeVectors(t)
	app := fakeApp(t, f)
	s := app.Settings()
	s.Processing.EmbeddingBatchSize = 7
	data, _ := json.Marshal(s)
	r := httptest.NewRequest(http.MethodPut, "http://localhost/api/settings", bytes.NewReader(data))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, r)
	if w.Code != 200 || app.Settings().Processing.EmbeddingBatchSize != 7 {
		t.Fatalf("API did not save batch setting: %d %s", w.Code, w.Body.String())
	}
	var saved Settings
	if err := app.store.get(context.Background(), "settings", "current", &saved); err != nil || saved.Processing.EmbeddingBatchSize != 7 {
		t.Fatal("batch setting was not persisted")
	}
}

func TestNestedArchiveEmbeddingBatch(t *testing.T) {
	f := newFakeVectors(t)
	app := fakeApp(t, f)
	setTestBatchSize(t, app, 2)
	fixtures, root := t.TempDir(), t.TempDir()
	images := make([][]byte, 3)
	for i := range images {
		writeBatchImage(t, fixtures, i)
		var err error
		images[i], err = os.ReadFile(filepath.Join(fixtures, fmt.Sprintf("%04d.jpg", i)))
		if err != nil {
			t.Fatal(err)
		}
	}
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for _, entry := range []struct {
		name string
		data []byte
	}{
		{"a.jpg", images[0]},
		{"nested.zip", makeZIP(t, "photos/b.jpg", images[1])},
		{"z.jpg", images[2]},
	} {
		out, err := w.Create(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := out.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "photos.zip"), b.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	source, err := app.AddSource(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	job := scanTestSource(t, app, source)
	if job.Status != "succeeded" || job.Indexed != 3 || !reflect.DeepEqual(batchSizes(f), []int{2, 1}) {
		t.Fatalf("nested archive batching: %+v %v", job, batchSizes(f))
	}
	results := searchTestAPI(t, app, SearchRequest{Query: "photo"})
	deep := false
	for _, result := range results {
		if len(result.Asset.Location.ArchiveEntries) == 2 {
			deep = true
		}
	}
	if len(results) != 3 || !deep {
		t.Fatal("nested archive locations lost")
	}
}

func TestBatchDiagnosticsSeparateRequestsAndImages(t *testing.T) {
	logger, logs := testLogger(t, "debug")
	f := newFakeVectors(t)
	app := loggedApp(t, f, logger)
	setTestBatchSize(t, app, 2)
	root := t.TempDir()
	for i := 0; i < 3; i++ {
		writeBatchImage(t, root, i)
	}
	source, err := app.AddSource(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	job := scanTestSource(t, app, source)
	finished := logs.wait(t, "job.finished", job.ID)
	if finished["embedding_calls"] != float64(2) || finished["embedded_images"] != float64(3) {
		t.Fatalf("batch counters ambiguous: %+v", finished)
	}
	sizes := []int{}
	for _, record := range logs.records(t) {
		if record["event"] == "dependency.finished" && record["operation"] == "embedding.image" && record["job_id"] == job.ID {
			if record["profile_id"] != job.ProfileID || record["run_id"] == nil {
				t.Fatal("batch correlation missing")
			}
			size, ok := record["batch_size"].(float64)
			if !ok {
				t.Fatal("batch size omitted from diagnostic output")
			}
			sizes = append(sizes, int(size))
		}
	}
	if !reflect.DeepEqual(sizes, []int{2, 1}) {
		t.Fatalf("batch request diagnostics: %v", sizes)
	}
}

func writeBatchImage(t *testing.T, root string, i int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	c := color.RGBA{R: uint8(i % 10 * 25), G: uint8(i / 10 % 10 * 25), B: uint8(i / 100 * 100), A: 255}
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("%04d.jpg", i)), b.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
}

func setTestBatchSize(t *testing.T, app *App, size int) {
	t.Helper()
	s := app.Settings()
	s.Processing.EmbeddingBatchSize = size
	if err := app.SaveSettings(context.Background(), s); err != nil {
		t.Fatal(err)
	}
}

func batchSizes(f *fakeVectors) []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.batchSizes...)
}

func TestIndexEmbeddingBatches(t *testing.T) {
	for _, tc := range []struct {
		size, count int
		want        []int
	}{
		{100, 205, []int{100, 100, 5}},
		{2, 5, []int{2, 2, 1}},
		{1, 3, []int{1, 1, 1}},
	} {
		t.Run(fmt.Sprint(tc.size), func(t *testing.T) {
			f := newFakeVectors(t)
			app := fakeApp(t, f)
			setTestBatchSize(t, app, tc.size)
			root := t.TempDir()
			for i := 0; i < tc.count; i++ {
				writeBatchImage(t, root, i)
			}
			source, err := app.AddSource(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			job := scanTestSource(t, app, source)
			if job.Status != "succeeded" || job.Indexed != tc.count || job.Embedded != tc.count || job.EmbeddingRequests != len(tc.want) {
				t.Fatalf("batch counters/results: %+v", job)
			}
			if got := batchSizes(f); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("batch sizes: got %v, want %v", got, tc.want)
			}
			if results := searchTestAPI(t, app, SearchRequest{Query: "color", Limit: 100}); len(results) != min(100, tc.count) {
				t.Fatal("batch result not searchable")
			}
			second := scanTestSource(t, app, source)
			if second.Status != "succeeded" || second.Skipped != tc.count || second.Embedded != 0 || second.EmbeddingRequests != 0 {
				t.Fatalf("unchanged files were re-embedded: %+v", second)
			}
		})
	}
}

func TestEmbeddingBatchResponseValidation(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		valid          bool
	}{
		{"reordered", `{"data":[{"index":1,"embedding":[2,3]},{"index":0,"embedding":[1,4]}]}`, true},
		{"missing", `{"data":[{"index":0,"embedding":[1,2]}]}`, false},
		{"duplicate", `{"data":[{"index":0,"embedding":[1,2]},{"index":0,"embedding":[2,3]}]}`, false},
		{"missing_index", `{"data":[{"embedding":[1,2]},{"index":1,"embedding":[2,3]}]}`, false},
		{"negative_index", `{"data":[{"index":-1,"embedding":[1,2]},{"index":1,"embedding":[2,3]}]}`, false},
		{"out_of_range", `{"data":[{"index":2,"embedding":[1,2]},{"index":1,"embedding":[2,3]}]}`, false},
		{"empty_vector", `{"data":[{"index":0,"embedding":[]},{"index":1,"embedding":[2,3]}]}`, false},
		{"zero_vector", `{"data":[{"index":0,"embedding":[0,0]},{"index":1,"embedding":[2,3]}]}`, false},
		{"overflow", `{"data":[{"index":0,"embedding":[1e100,0]},{"index":1,"embedding":[2,3]}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Model string `json:"model"`
					Input []struct {
						Content []struct {
							Type string `json:"type"`
							URL  struct {
								URL string `json:"url"`
							} `json:"image_url"`
						} `json:"content"`
					} `json:"input"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if len(body.Input) != 2 || body.Model != "embed" {
					t.Errorf("wrong batch request: %+v", body)
				}
				for i, input := range body.Input {
					want := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString([]byte{byte(i + 1)})
					if len(input.Content) != 1 || input.Content[0].Type != "image_url" || input.Content[0].URL.URL != want {
						t.Error("images combined or reordered in request")
					}
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.response))
			}))
			defer server.Close()
			settings := DefaultSettings()
			settings.Embedding.BaseURL = server.URL
			cfg, err := resolve(settings)
			if err != nil {
				t.Fatal(err)
			}
			vectors, err := newRemote(cfg).embedImages(context.Background(), [][]byte{{1}, {2}})
			if (err == nil) != tc.valid {
				t.Fatalf("validation: %v", err)
			}
			if tc.valid && !reflect.DeepEqual(vectors, [][]float32{{1, 4}, {2, 3}}) {
				t.Fatalf("response indices not respected: %v", vectors)
			}
		})
	}
}

func TestFailedBatchRetainsPreviousAssets(t *testing.T) {
	for _, dimensions := range []bool{false, true} {
		t.Run(fmt.Sprint(dimensions), func(t *testing.T) {
			f := newFakeVectors(t)
			app := fakeApp(t, f)
			root := t.TempDir()
			writeBatchImage(t, root, 0)
			writeBatchImage(t, root, 1)
			source, err := app.AddSource(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			if job := scanTestSource(t, app, source); job.Status != "succeeded" {
				t.Fatal(job)
			}
			before, err := listRecords[Asset](context.Background(), app.store, "asset")
			if err != nil {
				t.Fatal(err)
			}
			// A failed batch must neither publish replacements nor sweep an unseen old file.
			if err := os.Remove(filepath.Join(root, "0001.jpg")); err != nil {
				t.Fatal(err)
			}
			writeBatchImage(t, root, 2)
			writeBatchImage(t, root, 3)
			if dimensions {
				f.wrongDimension.Store(true)
			} else {
				f.rejectBatch.Store(true)
			}
			setTestBatchSize(t, app, 2)
			job := scanTestSource(t, app, source)
			if job.Status != "failed" || job.IssueCount != 2 || job.Indexed != 0 || job.EmbeddingRequests != 1 {
				t.Fatalf("failed batch: %+v", job)
			}
			after, err := listRecords[Asset](context.Background(), app.store, "asset")
			if err != nil || len(after) != len(before) {
				t.Fatalf("previous catalog swept: %v %v", after, err)
			}
			for i := range before {
				if before[i].Revision != after[i].Revision || before[i].ID != after[i].ID {
					t.Fatal("failed batch replaced committed assets")
				}
			}
		})
	}
}

func TestBatchSettingsSnapshotAndCancellation(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(fmt.Sprint(cancel), func(t *testing.T) {
			f := newFakeVectors(t)
			f.blockImage.Store(true)
			app := fakeApp(t, f)
			setTestBatchSize(t, app, 2)
			root := t.TempDir()
			for i := 0; i < 5; i++ {
				writeBatchImage(t, root, i)
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
				t.Fatal("batch did not start")
			}
			setTestBatchSize(t, app, 1)
			if cancel {
				if err := app.CancelJob(context.Background(), job.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				f.blockImage.Store(false)
				close(f.releaseImage)
			}
			finished := awaitJob(t, app, job.ID)
			if cancel {
				assets, err := listRecords[Asset](context.Background(), app.store, "asset")
				if finished.Status != "canceled" || err != nil || len(assets) != 0 {
					t.Fatalf("canceled batch committed files: %+v %v", finished, err)
				}
			} else {
				if finished.Status != "succeeded" || !reflect.DeepEqual(batchSizes(f), []int{2, 2, 1}) {
					t.Fatalf("running job lost snapshot: %+v %v", finished, batchSizes(f))
				}
				next, err := app.StartJob(context.Background(), source.ID, true)
				if err != nil {
					t.Fatal(err)
				}
				if done := awaitJob(t, app, next.ID); done.Status != "succeeded" || done.EmbeddingRequests != 5 {
					t.Fatalf("new job ignored changed batch size: %+v", done)
				}
			}
		})
	}
}

func TestVideoFrameEmbeddingBatches(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	f := newFakeVectors(t)
	app := fakeApp(t, f)
	s := app.Settings()
	s.Processing.EmbeddingBatchSize = 2
	s.Processing.VideoFrameChangeThreshold = 0
	if err := app.SaveSettings(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := runMedia(ctx, s.Processing, "ffmpeg", "-nostdin", "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=128x96:rate=1", "-t", "5", "-c:v", "libx264", "-g", "1", "-bf", "0", filepath.Join(root, "sample.mp4")); err != nil {
		t.Fatal(err)
	}
	source, err := app.AddSource(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	job := scanTestSource(t, app, source)
	if job.Status != "succeeded" || job.Indexed != 1 || !reflect.DeepEqual(batchSizes(f), []int{2, 2, 1}) {
		t.Fatalf("video batching: %+v %v", job, batchSizes(f))
	}
	assets, err := listRecords[Asset](context.Background(), app.store, "asset")
	if err != nil || len(assets) != 1 || len(assets[0].Points) != 5 {
		t.Fatalf("frame refs lost: %+v %v", assets, err)
	}
	for i, point := range assets[0].Points {
		if point.TimestampMS == nil || *point.TimestampMS != int64(i*1000) {
			t.Fatalf("frame timestamp reordered: %+v", point)
		}
	}
}
