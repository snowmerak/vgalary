package backend

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (a *App) indexSource(ctx context.Context, job *Job) error {
	var source Source
	if err := a.store.get(ctx, "source", job.SourceID, &source); err != nil {
		return err
	}
	if !source.Active {
		return errors.New("source is removed")
	}
	cfg, err := resolve(job.Settings)
	if err != nil {
		return err
	}
	remote := newRemote(cfg)
	vector, err := remote.embedText(ctx, "media search")
	if err != nil {
		return err
	}
	// The vector-space fingerprint is independent of location, but index state belongs to a target.
	profileID := cfg.profileID() + "_" + digest([]byte(cfg.QdrantURL + "/" + cfg.Qdrant.CollectionPrefix))[:8]
	collection := cfg.Qdrant.CollectionPrefix + "_" + profileID
	profile := Profile{ID: profileID, Collection: collection, Dimensions: len(vector), Settings: job.Settings, Model: cfg.Model}
	if err := remote.ensureCollection(ctx, collection, len(vector)); err != nil {
		return fmt.Errorf("qdrant: %w", err)
	}
	if err := a.store.put(ctx, "profile", profileID, profile); err != nil {
		return err
	}
	job.ProfileID = profileID
	work, err := os.MkdirTemp(filepath.Join(a.dir, "tmp"), "scan-"+job.ID+"-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()
	// Bound vector reuse separately from decoded media. Nothing here is durable vector storage.
	reused := map[string][]float32{}
	report := walkSource(ctx, source, work, cfg.Processing, func(file discoveredFile) error {
		job.Discovered++
		err := a.indexFile(ctx, source, file, work, profile, remote, job, reused)
		if saveErr := a.store.put(ctx, "job", job.ID, job); saveErr != nil {
			return saveErr
		}
		return err
	})
	job.Cycles = report.Cycles
	job.IssueCount = len(report.Issues)
	job.Issues = report.Issues[:min(len(report.Issues), 100)]
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !report.Complete {
		return fmt.Errorf("scan incomplete: %d issue(s); previous unseen assets retained", len(report.Issues))
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.store.get(ctx, "source", source.ID, &source); err != nil {
		return err
	}
	if !source.Active {
		return errors.New("source removed during scan")
	}
	assets, err := listRecords[Asset](ctx, a.store, "asset")
	if err != nil {
		return err
	}
	for _, asset := range assets {
		if asset.SourceID == source.ID && asset.ProfileID == profileID && asset.SeenJob != job.ID {
			if err := a.dropAsset(ctx, asset); err != nil {
				return err
			}
		}
	}
	if err := a.store.put(ctx, "source_profile", source.ID+"_"+profileID, true); err != nil {
		return err
	}
	var activeProfile string
	if err := a.store.get(ctx, "meta", "active_profile", &activeProfile); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if activeProfile != "" && activeProfile != profileID {
		sources, err := a.Sources(ctx)
		if err != nil {
			return err
		}
		for _, registered := range sources {
			var ready bool
			if err := a.store.get(ctx, "source_profile", registered.ID+"_"+profileID, &ready); errors.Is(err, sql.ErrNoRows) {
				return nil
			} else if err != nil {
				return err
			}
			if !ready {
				return nil
			}
		}
	}
	return a.store.put(ctx, "meta", "active_profile", profileID)
}

func (a *App) indexFile(ctx context.Context, source Source, file discoveredFile, work string, profile Profile, remote *remoteClient, job *Job, reused map[string][]float32) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := a.store.get(ctx, "source", source.ID, &source); err != nil {
		return err
	}
	if !source.Active {
		return errors.New("source is removed")
	}
	outerPath := filepath.Join(source.RootPath, file.Location.RelativePath)
	outer, err := os.Stat(outerPath)
	if err != nil {
		return err
	}
	original, err := os.Stat(file.Path)
	if err != nil {
		return err
	}
	revision, err := hashFile(ctx, file.Path)
	if err != nil {
		return err
	}
	asset := Asset{ID: stableID(source.ID, file.Location, profile.ID), SourceID: source.ID, Location: file.Location, MediaType: mediaType(file.Path), Revision: revision, ProfileID: profile.ID, Collection: profile.Collection, Size: original.Size(), OuterSize: outer.Size(), OuterModTime: outer.ModTime().UnixNano(), SeenJob: job.ID, UpdatedAt: time.Now().UTC()}
	var previous Asset
	priorErr := a.store.get(ctx, "asset", asset.ID, &previous)
	if priorErr != nil && !errors.Is(priorErr, sql.ErrNoRows) {
		return priorErr
	}
	if !job.Force && priorErr == nil && previous.Revision == revision {
		// Qdrant may have been restored independently from the SQLite catalog.
		allPresent := true
		for _, point := range previous.Points {
			if err := remote.qdrant(ctx, "GET", collectionPath(profile.Collection)+"/points/"+point.ID, nil, new(any)); errors.Is(err, errRemoteNotFound) {
				allPresent = false
				break
			} else if err != nil {
				return err
			}
		}
		if allPresent {
			asset.Points = previous.Points
			if err := a.commitAsset(ctx, asset, nil); err != nil {
				return err
			}
			job.Skipped++
			return nil
		}
	}
	refs := []PointRef{{StreamIndex: 0}}
	images := []string{file.Path}
	var reuse *Asset
	if !job.Force {
		assets, err := listRecords[Asset](ctx, a.store, "asset")
		if err != nil {
			return err
		}
		for _, candidate := range assets {
			if candidate.ID != asset.ID && candidate.ProfileID == profile.ID && candidate.Revision == revision && candidate.MediaType == asset.MediaType && len(candidate.Points) > 0 {
				if _, err := remote.vector(ctx, profile.Collection, candidate.Points[0].ID); errors.Is(err, errRemoteNotFound) {
					continue
				} else if err != nil {
					return err
				}
				reuse = &candidate
				refs = append([]PointRef(nil), candidate.Points...)
				break
			}
		}
	}
	if asset.MediaType == "video" && reuse == nil {
		framesDir, err := os.MkdirTemp(work, "frames-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(framesDir) }()
		refs, images, err = videoFrames(ctx, file.Path, framesDir, remote.cfg.Processing)
		if err != nil {
			return err
		}
	}
	priorPoints := map[string]bool{}
	for _, point := range previous.Points {
		priorPoints[point.ID] = true
	}
	pending := cleanupTask{ID: "pending-" + asset.ID + "-" + revision, Collection: profile.Collection, Settings: profile.Settings, Points: []string{}}
	for i := range refs {
		refs[i].ID = stableID(asset.ID, revision, refs[i].PTS, refs[i].SelectionKind)
		if !priorPoints[refs[i].ID] {
			pending.Points = append(pending.Points, refs[i].ID)
		}
	}
	if err := a.store.put(ctx, "cleanup", pending.ID, pending); err != nil {
		return err
	}
	for i := range refs {
		var jpeg []byte
		var vector []float32
		if reuse != nil {
			vector, err = remote.vector(ctx, profile.Collection, reuse.Points[i].ID)
			if err != nil {
				return err
			}
			jpeg, _ = os.ReadFile(filepath.Join(a.dir, "thumbnails", reuse.Points[i].ID+".jpg"))
		} else {
			jpeg, err = imageJPEG(images[i], remote.cfg.Processing)
			if err != nil {
				return err
			}
			cacheKey := digest(jpeg)
			var ok bool
			vector, ok = reused[cacheKey]
			if !ok {
				vector, err = remote.embedImage(ctx, jpeg)
				if err != nil {
					return err
				}
				job.Embedded++
				if len(reused) >= 64 {
					clear(reused)
				}
				reused[cacheKey] = vector
			}
		}
		if len(vector) != profile.Dimensions {
			return errors.New("embedding vector dimension changed")
		}
		payload := map[string]any{"source_id": source.ID, "asset_id": asset.ID, "asset_revision": revision, "profile_id": profile.ID, "media_type": asset.MediaType, "relative_path": asset.Location.RelativePath, "archive_entries": asset.Location.ArchiveEntries}
		if refs[i].TimestampMS != nil {
			payload["timestamp_ms"] = *refs[i].TimestampMS
			payload["pts"] = refs[i].PTS
			payload["time_base"] = refs[i].TimeBase
			payload["selection_kind"] = refs[i].SelectionKind
			payload["stream_index"] = refs[i].StreamIndex
			if refs[i].ChangeScore != nil {
				payload["change_score"] = *refs[i].ChangeScore
			}
		}
		if err := remote.upsert(ctx, profile.Collection, refs[i].ID, vector, payload); err != nil {
			return err
		}
		if len(jpeg) > 0 {
			if err := a.saveThumbnail(refs[i].ID, jpeg); err != nil {
				return err
			}
		}
		if err := a.store.put(ctx, "job", job.ID, job); err != nil {
			return err
		}
	}
	asset.Points = refs
	after, err := os.Stat(file.Path)
	if err != nil {
		return err
	}
	if after.Size() != original.Size() || !after.ModTime().Equal(original.ModTime()) {
		return errors.New("media changed during indexing")
	}
	if err := a.checkOriginal(asset, source); err != nil {
		return err
	}
	var cleanup *cleanupTask
	if priorErr == nil && previous.Revision != revision {
		task := cleanupFor(previous, profile.Settings)
		cleanup = &task
	}
	if err := a.commitAsset(ctx, asset, cleanup); err != nil {
		return err
	}
	job.Indexed++
	return nil
}
func (a *App) commitAsset(ctx context.Context, asset Asset, cleanup *cleanupTask) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	var source Source
	if err := a.store.get(ctx, "source", asset.SourceID, &source); err != nil {
		return err
	}
	if !source.Active {
		return errors.New("source is removed")
	}
	return a.store.changeAsset(ctx, asset, false, cleanup)
}
func (a *App) saveThumbnail(id string, b []byte) error {
	filename := filepath.Join(a.dir, "thumbnails", id+".jpg")
	if _, err := os.Stat(filename); err == nil {
		return nil
	}
	tmp, err := os.CreateTemp(filepath.Join(a.dir, "thumbnails"), ".thumbnail-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	_, writeErr := tmp.Write(b)
	closeErr := tmp.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp.Name(), filename)
}

func (a *App) Search(ctx context.Context, request SearchRequest) ([]SearchResult, error) {
	if strings.TrimSpace(request.Query) == "" || len(request.Query) > 16000 {
		return nil, errors.New("query must contain 1..16000 bytes")
	}
	if request.Limit == 0 {
		request.Limit = 20
	}
	if request.Limit < 1 || request.Limit > 100 || request.Offset < 0 || request.Offset > 10000 {
		return nil, errors.New("invalid search limit/offset")
	}
	if request.MediaType != "" && request.MediaType != "image" && request.MediaType != "video" {
		return nil, errors.New("invalid media_type")
	}
	var profileID string
	if err := a.store.get(ctx, "meta", "active_profile", &profileID); err != nil {
		return nil, errors.New("no active index; complete a source scan first")
	}
	var profile Profile
	if err := a.store.get(ctx, "profile", profileID, &profile); err != nil {
		return nil, err
	}
	cfg, err := resolve(profile.Settings)
	// Credentials and endpoint settings can change without changing the vector space.
	// Keep the previous model/profile when a new index is still being prepared.
	if current, currentErr := resolve(a.Settings()); currentErr == nil {
		currentID := current.profileID() + "_" + digest([]byte(current.QdrantURL + "/" + current.Qdrant.CollectionPrefix))[:8]
		if currentID == profile.ID {
			cfg = current
			err = nil
		}
	}
	if err != nil {
		return nil, err
	}
	cfg.Model = profile.Model
	remote := newRemote(cfg)
	vector, err := remote.embedText(ctx, request.Query)
	if err != nil {
		return nil, err
	}
	if len(vector) != profile.Dimensions {
		return nil, errors.New("query dimension does not match active index")
	}
	assets, err := listRecords[Asset](ctx, a.store, "asset")
	if err != nil {
		return nil, err
	}
	sources, err := a.Sources(ctx)
	if err != nil {
		return nil, err
	}
	sourceMap := map[string]Source{}
	for _, s := range sources {
		sourceMap[s.ID] = s
	}
	type candidate struct {
		asset Asset
		point PointRef
	}
	points := map[string]candidate{}
	for _, asset := range assets {
		if asset.ProfileID != profile.ID {
			continue
		}
		if _, ok := sourceMap[asset.SourceID]; !ok {
			continue
		}
		for _, point := range asset.Points {
			points[point.ID] = candidate{asset, point}
		}
	}
	best := map[string]SearchResult{}
	// Refill after stale-point filtering and video grouping, with a bounded candidate budget.
	for offset := 0; offset < 10000; offset += 256 {
		hits, err := remote.query(ctx, profile.Collection, vector, request.SourceID, request.MediaType, 256, offset)
		if err != nil {
			return nil, fmt.Errorf("qdrant search: %w", err)
		}
		for _, hit := range hits {
			entry, ok := points[hit.ID]
			if !ok {
				continue
			}
			if _, exists := best[entry.asset.ID]; exists {
				continue
			}
			source := sourceMap[entry.asset.SourceID]
			result := SearchResult{Asset: entry.asset, Score: hit.Score, PointID: hit.ID, TimestampMS: entry.point.TimestampMS, SelectionKind: entry.point.SelectionKind, Path: locationText(Location{RelativePath: filepath.Join(source.RootPath, entry.asset.Location.RelativePath), ArchiveEntries: entry.asset.Location.ArchiveEntries})}
			if entry.point.TimestampMS != nil {
				result.Timestamp = formatTimestamp(*entry.point.TimestampMS)
			}
			if _, err := os.Stat(filepath.Join(a.dir, "thumbnails", hit.ID+".jpg")); err == nil {
				result.ThumbnailURL = "/api/assets/" + entry.asset.ID + "/thumbnail?point_id=" + hit.ID
			}
			best[entry.asset.ID] = result
		}
		if len(best) >= request.Offset+request.Limit || len(hits) < 256 {
			break
		}
	}
	results := make([]SearchResult, 0, len(best))
	for _, result := range best {
		results = append(results, result)
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].Asset.ID < results[j].Asset.ID
		}
		return results[i].Score > results[j].Score
	})
	start := min(request.Offset, len(results))
	end := min(start+request.Limit, len(results))
	return results[start:end], nil
}
func formatTimestamp(ms int64) string {
	return fmt.Sprintf("%02d:%02d:%02d.%03d", ms/3600000, (ms/60000)%60, (ms/1000)%60, ms%1000)
}
