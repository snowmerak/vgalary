package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func catalogTestApp(t *testing.T, count int) (*App, []Asset) {
	t.Helper()
	app, err := New(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	ctx := context.Background()
	media := t.TempDir()
	for _, id := range []string{"source-a", "source-b", "empty", "removed"} {
		root := filepath.Join(media, id)
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := app.store.put(ctx, "source", id, Source{ID: id, RootPath: root, Active: id != "removed"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := app.store.put(ctx, "meta", "active_profile", "current"); err != nil {
		t.Fatal(err)
	}
	assets := []Asset{}
	for index := 0; index < count; index++ {
		source, kind := "source-a", "image"
		if index%2 != 0 {
			source = "source-b"
		}
		if index%3 == 0 {
			kind = "video"
		}
		asset := Asset{ID: fmt.Sprintf("asset-%03d", index), SourceID: source, MediaType: kind, ProfileID: "current", UpdatedAt: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC).Add(-time.Duration(index/3) * time.Second)}
		if err := app.store.put(ctx, "asset", asset.ID, asset); err != nil {
			t.Fatal(err)
		}
		assets = append(assets, asset)
	}
	return app, assets
}

func catalogGet(t *testing.T, app *App, path string, out any) int {
	t.Helper()
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil))
	if out != nil {
		if response.Code != 200 {
			t.Fatalf("%s: %d %s", path, response.Code, response.Body.String())
		}
		if err := json.Unmarshal(response.Body.Bytes(), out); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
	return response.Code
}

func TestCatalogServerPaginationAndCounts(t *testing.T) {
	app, expected := catalogTestApp(t, 65)
	ctx := context.Background()
	for _, hidden := range []Asset{
		{ID: "old", SourceID: "source-a", ProfileID: "old", MediaType: "image"},
		{ID: "removed", SourceID: "removed", ProfileID: "current", MediaType: "image"},
		{ID: "orphan", SourceID: "missing", ProfileID: "current", MediaType: "image"},
	} {
		if err := app.store.put(ctx, "asset", hidden.ID, hidden); err != nil {
			t.Fatal(err)
		}
	}
	var first []Asset
	catalogGet(t, app, "/api/assets", &first)
	if len(first) != 24 {
		t.Fatalf("default page must be bounded: %d", len(first))
	}
	seen := map[string]bool{}
	for offset := 0; offset < 65; offset += 24 {
		var page []Asset
		catalogGet(t, app, fmt.Sprintf("/api/assets?limit=24&offset=%d", offset), &page)
		if len(page) != min(24, 65-offset) {
			t.Fatalf("offset %d returned %d items", offset, len(page))
		}
		for index, asset := range page {
			if seen[asset.ID] || asset.ID != expected[offset+index].ID {
				t.Fatalf("unstable order, duplicate or missing asset: %+v", asset)
			}
			seen[asset.ID] = true
		}
	}
	var matching []Asset
	for _, asset := range expected {
		if asset.SourceID == "source-b" && asset.MediaType == "image" {
			matching = append(matching, asset)
		}
	}
	var filtered []Asset
	catalogGet(t, app, "/api/assets?source_id=source-b&media_type=image&limit=5&offset=5", &filtered)
	if len(filtered) != 5 {
		t.Fatalf("filters must apply before pagination: %+v", filtered)
	}
	for i := range filtered {
		if filtered[i].ID != matching[i+5].ID {
			t.Fatal("wrong filtered page")
		}
	}
	var old []Asset
	catalogGet(t, app, "/api/assets?profile_id=old", &old)
	if len(old) != 1 || old[0].ID != "old" {
		t.Fatalf("explicit profile filter broken: %+v", old)
	}
	var counts map[string]int
	catalogGet(t, app, "/api/assets/counts", &counts)
	if len(counts) != 3 || counts["source-a"] != 33 || counts["source-b"] != 32 || counts["empty"] != 0 {
		t.Fatalf("counts must cover the full visible catalog without removed sources: %+v", counts)
	}
	for _, query := range []string{"limit=0", "limit=101", "limit=-1", "limit=nope", "offset=-1", "offset=nope", "media_type=audio"} {
		if got := catalogGet(t, app, "/api/assets?"+query, nil); got != 400 {
			t.Fatalf("invalid pagination/filter %s: %d", query, got)
		}
	}
	for _, query := range []string{"offset=9223372036854775807", "source_id=empty", "source_id=removed", "source_id=" + url.QueryEscape("' OR 1=1 --")} {
		var page []Asset
		catalogGet(t, app, "/api/assets?"+query, &page)
		if page == nil || len(page) != 0 {
			t.Fatalf("expected an empty JSON array for %s: %+v", query, page)
		}
	}
	// An asset with malformed frame metadata outside the page must not be decoded
	// or transferred. Counts only aggregate metadata, including this record.
	if err := app.store.put(ctx, "asset", "zz-bad-frames", map[string]any{"id": "zz-bad-frames", "source_id": "source-a", "profile_id": "current", "media_type": "video", "updated_at": "2000-01-01T00:00:00Z", "points": "invalid"}); err != nil {
		t.Fatal(err)
	}
	catalogGet(t, app, "/api/assets?limit=24", &first)
	catalogGet(t, app, "/api/assets/counts", &counts)
	if len(first) != 24 || counts["source-a"] != 34 {
		t.Fatal("listing or counting decoded assets outside the requested page")
	}
	if got := catalogGet(t, app, "/api/assets?offset=65&limit=1", nil); got != 500 {
		t.Fatalf("requested malformed record should fail decoding: %d", got)
	}
}

func TestCatalogIndexesPreserveExistingJSONRecords(t *testing.T) {
	dir := t.TempDir()
	store, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.put(ctx, "asset", "legacy", Asset{ID: "legacy", ProfileID: "old"}); err != nil {
		t.Fatal(err)
	}
	var before []byte
	if err := store.db.QueryRow(`SELECT body FROM records WHERE kind='asset' AND id='legacy'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`DROP INDEX catalog_asset_order; DROP INDEX catalog_asset_source;`); err != nil {
		t.Fatal(err)
	}
	if err := store.db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.db.Close() }()
	var after []byte
	var indexes int
	if err := reopened.db.QueryRow(`SELECT body FROM records WHERE kind='asset' AND id='legacy'`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if err := reopened.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name IN ('catalog_asset_order','catalog_asset_source')`).Scan(&indexes); err != nil {
		t.Fatal(err)
	}
	if indexes != 2 || !bytes.Equal(before, after) {
		t.Fatal("adding pagination indexes must preserve existing record bytes")
	}
}
