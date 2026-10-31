package backend

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Run explicitly: VGALARY_LIVE_TEST=1 with VGALARY_FIXTURE_ROOT pointing at mkfixtures output.
// No fixture is uploaded anywhere except the embedding endpoint selected in settings.
func TestLiveBackend(t *testing.T) {
	if os.Getenv("VGALARY_LIVE_TEST") != "1" {
		t.Skip("set VGALARY_LIVE_TEST=1 to use real llama.cpp and Qdrant")
	}
	root := os.Getenv("VGALARY_FIXTURE_ROOT")
	if root == "" {
		t.Fatal("VGALARY_FIXTURE_ROOT is required")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	dataDir := os.Getenv("VGALARY_TEST_DATA_DIR")
	persistent := dataDir != ""
	if !persistent {
		dataDir = t.TempDir()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	app, err := New(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if app != nil {
			if err := app.Close(); err != nil {
				t.Error(err)
			}
		}
	}()
	settings := app.Settings()
	settings.Processing.AllowKeyframeFallback = true
	if !persistent {
		settings.Qdrant.CollectionPrefix = fmt.Sprintf("vgalary_test_%d", time.Now().UnixNano())
	}
	if v := os.Getenv("VGALARY_TEST_EMBEDDING_URL"); v != "" {
		settings.Embedding.BaseURL = v
	}
	if v := os.Getenv("VGALARY_TEST_QDRANT_URL"); v != "" {
		settings.Qdrant.URL = v
	}
	if err := app.SaveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(app.Handler())
	defer func() { server.Close() }()
	call := func(method, path string, body, out any) {
		t.Helper()
		var payload []byte
		if body != nil {
			var err error
			payload, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
		req, err := http.NewRequestWithContext(ctx, method, server.URL+path, bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			data, _ := io.ReadAll(response.Body)
			t.Fatalf("%s %s: %d %s", method, path, response.StatusCode, data)
		}
		if out != nil {
			if err := json.NewDecoder(response.Body).Decode(out); err != nil {
				t.Fatal(err)
			}
		}
	}
	var connection map[string]any
	call("POST", "/api/connections/test", nil, &connection)
	t.Logf("connection: %+v", connection)
	var source Source
	registered, err := app.Sources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range registered {
		if candidate.RootPath == root {
			source = candidate
			break
		}
	}
	if source.ID == "" {
		call("POST", "/api/sources", map[string]string{"root_path": root}, &source)
	}
	waitJob := func(id string) Job {
		t.Helper()
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		lastLog := time.Now()
		for {
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-ticker.C:
			}
			var job Job
			call("GET", "/api/jobs/"+id, nil, &job)
			if time.Since(lastLog) > 10*time.Second {
				t.Logf("job %s: discovered=%d indexed=%d skipped=%d embedded=%d", job.Status, job.Discovered, job.Indexed, job.Skipped, job.Embedded)
				lastLog = time.Now()
			}
			if job.Status == "failed" || job.Status == "canceled" {
				t.Fatalf("job %s: %s issues=%+v", job.Status, job.Error, job.Issues)
			}
			if job.Status == "succeeded" {
				return job
			}
		}
	}
	started := time.Now()
	var queued Job
	call("POST", "/api/sources/"+source.ID+"/scan", map[string]bool{"force": false}, &queued)
	first := waitJob(queued.ID)
	t.Logf("initial scan: assets=%d image embedding calls=%d elapsed=%s", first.Indexed, first.Embedded, time.Since(started))
	var assets []Asset
	call("GET", "/api/assets?source_id="+source.ID, nil, &assets)
	if len(assets) != 10 {
		t.Fatalf("expected 10 logical media locations; got %d", len(assets))
	}
	images, videos, points, maxDepth := 0, 0, 0, 0
	var deepest Asset
	for _, asset := range assets {
		maxDepth = max(maxDepth, len(asset.Location.ArchiveEntries))
		if len(asset.Location.ArchiveEntries) > len(deepest.Location.ArchiveEntries) {
			deepest = asset
		}
		points += len(asset.Points)
		if asset.MediaType == "image" {
			images++
			for _, p := range asset.Points {
				if p.TimestampMS != nil {
					t.Fatal("image has video timestamp")
				}
			}
		} else {
			videos++
			if len(asset.Points) < 2 {
				t.Fatal("video keyframes missing")
			}
			for index, p := range asset.Points {
				if p.TimestampMS == nil || p.PTS == "" || p.TimeBase == "" || p.SelectionKind != "random_access_keyframe" {
					t.Fatalf("AV1 keyframe metadata missing or mislabeled: %+v", p)
				}
				if settings.Processing.VideoFrameChangeThreshold > 0 && index > 0 && (p.ChangeScore == nil || *p.ChangeScore < settings.Processing.VideoFrameChangeThreshold) {
					t.Fatal("video point bypassed scene-change filter")
				}
			}
		}
	}
	if images != 7 || videos != 3 || maxDepth < 10 {
		t.Fatalf("missing nested media: images=%d videos=%d max_depth=%d", images, videos, maxDepth)
	}
	queries := []string{"손에 들고 있는 파란 고양이 장난감", "가방에 달린 금색 캐릭터 배지", "가게에 걸린 커다란 캐릭터 수건"}
	searchEvidence := []map[string]any{}
	for _, query := range queries {
		var response struct {
			Results []SearchResult `json:"results"`
		}
		call("POST", "/api/search", SearchRequest{Query: query, Limit: 5, MediaType: "image"}, &response)
		if len(response.Results) == 0 {
			t.Fatal("image search returned no results")
		}
		t.Logf("query %q -> %s (%.4f)", query, response.Results[0].Path, response.Results[0].Score)
		searchEvidence = append(searchEvidence, map[string]any{"query": query, "top_path": response.Results[0].Path, "score": response.Results[0].Score})
	}
	var videoSearch struct {
		Results []SearchResult `json:"results"`
	}
	call("POST", "/api/search", SearchRequest{Query: "사람이 말하는 영상", MediaType: "video", Limit: 5}, &videoSearch)
	if len(videoSearch.Results) != 3 {
		t.Fatalf("video grouping returned %d results", len(videoSearch.Results))
	}
	for _, result := range videoSearch.Results {
		if result.TimestampMS == nil || result.Timestamp == "" || result.SelectionKind != "random_access_keyframe" {
			t.Fatal("video timestamp not returned")
		}
	}
	req, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/assets/"+deepest.ID+"/download", nil)
	if err != nil {
		t.Fatal(err)
	}
	download, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	_, copyErr := io.Copy(h, download.Body)
	_ = download.Body.Close()
	if copyErr != nil || download.StatusCode != 200 || hex.EncodeToString(h.Sum(nil)) != deepest.Revision {
		t.Fatal("nested original download hash mismatch")
	}
	// Preview must not unpack an archive after the index cache has gone away.
	point := deepest.Points[0]
	if err := os.Remove(filepath.Join(app.dir, "thumbnails", point.ID+".jpg")); err != nil {
		t.Fatal(err)
	}
	response, err := http.Get(server.URL + "/api/assets/" + deepest.ID + "/thumbnail")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 404 {
		t.Fatal("missing archive thumbnail regenerated on display")
	}
	response, err = http.Get(server.URL + "/api/assets/" + deepest.ID + "/content")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 409 {
		t.Fatal("archive preview unexpectedly materialized original")
	}
	call("POST", "/api/sources/"+source.ID+"/scan", map[string]bool{"force": false}, &queued)
	second := waitJob(queued.ID)
	if second.Embedded != 0 || second.Skipped != 10 {
		t.Fatalf("unchanged scan re-embedded media: %+v", second)
	}
	// An overlapping source retains its own locations after removing the other registration.
	var secondary Source
	call("POST", "/api/sources", map[string]string{"root_path": filepath.Join(root, "loose")}, &secondary)
	call("POST", "/api/sources/"+secondary.ID+"/scan", map[string]bool{"force": false}, &queued)
	waitJob(queued.ID)
	call("DELETE", "/api/sources/"+secondary.ID, nil, nil)
	var removedSearch struct {
		Results []SearchResult `json:"results"`
	}
	call("POST", "/api/search", SearchRequest{Query: "장난감", SourceID: secondary.ID}, &removedSearch)
	if len(removedSearch.Results) != 0 {
		t.Fatal("removed source still searchable")
	}
	if _, err := os.Stat(filepath.Join(root, "loose", "hand-held-toy.jpg")); err != nil {
		t.Fatal("source removal deleted original")
	}
	server.Close()
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	app = nil
	app, err = New(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	sources, err := app.Sources(ctx)
	if err != nil || len(sources) != 1 || sources[0].ID != source.ID {
		t.Fatal("source settings did not survive restart")
	}
	persisted, err := app.Search(ctx, SearchRequest{Query: "파란 인형", Limit: 3})
	if err != nil || len(persisted) == 0 {
		t.Fatalf("search did not survive restart: %v", err)
	}
	report := map[string]any{"verified_at": time.Now().UTC(), "images": images, "videos": videos, "points": points, "max_archive_depth": maxDepth, "first_image_embeddings": first.Embedded, "unchanged_image_embeddings": second.Embedded, "connection": connection, "queries": searchEvidence, "video_match_timestamp": videoSearch.Results[0].Timestamp, "download_sha256_verified": true, "removed_source_excluded": true, "settings_and_search_survived_restart": true}
	if persistent {
		b, _ := json.MarshalIndent(report, "", "  ")
		if err := os.WriteFile(filepath.Join(dataDir, "verification.json"), b, 0600); err != nil {
			t.Fatal(err)
		}
	} else {
		cfg, err := resolve(settings)
		if err != nil {
			t.Fatal(err)
		}
		profiles, err := listRecords[Profile](ctx, app.store, "profile")
		if err != nil {
			t.Fatal(err)
		}
		for _, profile := range profiles {
			if strings.HasPrefix(profile.Collection, settings.Qdrant.CollectionPrefix+"_") {
				if err := newRemote(cfg).qdrant(ctx, "DELETE", collectionPath(profile.Collection), nil, nil); err != nil {
					t.Error(err)
				}
			}
		}
	}
	t.Logf("verified images=%d videos=%d points=%d nested_depth=%d, rescan embedding calls=0", images, videos, points, maxDepth)
}
