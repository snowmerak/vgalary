package backend

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

type fileIndexPlan struct {
	ctx       context.Context
	source    Source
	file      discoveredFile
	asset     Asset
	original  os.FileInfo
	previous  *Asset
	refs      []PointRef
	images    []string
	reuse     *Asset
	framesDir string
	remaining int
	started   time.Time
	itemID    string
	done      bool
}

type batchTarget struct {
	file  *fileIndexPlan
	index int
}

type batchImage struct {
	jpeg    []byte
	key     string
	targets []batchTarget
}

// One scan owns this synchronous buffer. It batches across files and video
// frames without parallel mutation of job counters or file commit state.
type indexBatch struct {
	app     *App
	ctx     context.Context
	job     *Job
	profile Profile
	remote  *remoteClient
	report  scanReport
	queue   []batchImage
	keys    map[string]int
	cached  map[string][]float32
	files   map[*fileIndexPlan]struct{}
	targets int
	bytes   int
}

// Flush large images early, bounding the raw JPEG buffer independently of count.
// JSON/base64 encoding temporarily adds copies of these bytes.
const maxEmbeddingBatchBytes = 64 << 20

func newIndexBatch(app *App, ctx context.Context, job *Job, profile Profile, remote *remoteClient) *indexBatch {
	return &indexBatch{app: app, ctx: ctx, job: job, profile: profile, remote: remote, report: scanReport{Complete: true}, keys: map[string]int{}, cached: map[string][]float32{}, files: map[*fileIndexPlan]struct{}{}}
}

func (b *indexBatch) activate(file *fileIndexPlan) {
	if o := executionOf(file.ctx).observation; o != nil {
		o.mu.Lock()
		o.item, o.asset = file.itemID, file.asset.ID
		o.mu.Unlock()
	}
}

func (b *indexBatch) addFile(file *fileIndexPlan) {
	b.files[file] = struct{}{}
	for i := range file.refs {
		if file.done {
			break
		}
		b.activate(file)
		if err := file.ctx.Err(); err != nil {
			b.fail(file, err)
			break
		}
		if file.reuse != nil {
			vector, err := b.remote.vector(file.ctx, b.profile.Collection, file.reuse.Points[i].ID)
			if err != nil {
				b.fail(file, err)
				break
			}
			jpeg, _ := os.ReadFile(filepath.Join(b.app.dir, "thumbnails", file.reuse.Points[i].ID+".jpg"))
			b.publish(batchTarget{file, i}, jpeg, vector)
			continue
		}
		setStage(file.ctx, "image.decode")
		jpeg, err := imageJPEG(file.images[i], b.remote.cfg.Processing)
		if err != nil {
			b.fail(file, err)
			break
		}
		key := digest(jpeg)
		if vector, ok := b.cached[key]; ok {
			b.publish(batchTarget{file, i}, jpeg, vector)
			continue
		}
		if b.bytes+len(jpeg) > maxEmbeddingBatchBytes {
			b.flush()
			if file.done {
				break
			}
		}
		if index, ok := b.keys[key]; ok {
			b.queue[index].targets = append(b.queue[index].targets, batchTarget{file, i})
		} else {
			b.keys[key] = len(b.queue)
			b.queue = append(b.queue, batchImage{jpeg: jpeg, key: key, targets: []batchTarget{{file, i}}})
			b.bytes += len(jpeg)
		}
		b.targets++
		// Bound targets too: identical files must not accumulate without flushing.
		if b.targets >= b.remote.cfg.Processing.EmbeddingBatchSize {
			b.flush()
		}
	}
}

func (b *indexBatch) flush() {
	queue := b.queue
	b.queue = nil
	clear(b.keys)
	b.targets, b.bytes = 0, 0
	active := queue[:0]
	for _, image := range queue {
		for _, target := range image.targets {
			if !target.file.done {
				active = append(active, image)
				break
			}
		}
	}
	if len(active) == 0 {
		return
	}
	images := make([][]byte, len(active))
	for i := range active {
		images[i] = active[i].jpeg
	}
	var vectors [][]float32
	err := b.ctx.Err()
	if err == nil {
		b.job.EmbeddingRequests++
		vectors, err = b.remote.embedImages(b.ctx, images)
	}
	if err == nil {
		for _, vector := range vectors {
			if len(vector) != b.profile.Dimensions {
				err = errors.New("embedding vector dimension changed")
				break
			}
		}
	}
	if err != nil {
		for _, image := range active {
			for _, target := range image.targets {
				b.fail(target.file, err)
			}
		}
		return
	}
	b.job.Embedded += len(images)
	updateCounters(b.ctx, b.job)
	for i, image := range active {
		if len(b.cached) >= 64 {
			clear(b.cached)
		}
		b.cached[image.key] = vectors[i]
		for _, target := range image.targets {
			b.publish(target, image.jpeg, vectors[i])
		}
	}
}

func (b *indexBatch) publish(target batchTarget, jpeg []byte, vector []float32) {
	file := target.file
	if file.done {
		return
	}
	b.activate(file)
	err := b.storePoint(file, target.index, jpeg, vector)
	if err == nil {
		file.remaining--
		if file.remaining == 0 {
			err = b.commit(file)
		}
	}
	if err != nil {
		b.fail(file, err)
	}
}

func (b *indexBatch) storePoint(file *fileIndexPlan, i int, jpeg []byte, vector []float32) error {
	if len(vector) != b.profile.Dimensions {
		return errors.New("embedding vector dimension changed")
	}
	ref := file.refs[i]
	asset := file.asset
	payload := map[string]any{"source_id": asset.SourceID, "asset_id": asset.ID, "asset_revision": asset.Revision, "profile_id": asset.ProfileID, "media_type": asset.MediaType, "relative_path": asset.Location.RelativePath, "archive_entries": asset.Location.ArchiveEntries}
	if ref.TimestampMS != nil {
		payload["timestamp_ms"], payload["pts"], payload["time_base"] = *ref.TimestampMS, ref.PTS, ref.TimeBase
		payload["selection_kind"], payload["stream_index"] = ref.SelectionKind, ref.StreamIndex
		if ref.ChangeScore != nil {
			payload["change_score"] = *ref.ChangeScore
		}
	}
	if err := b.remote.upsert(file.ctx, b.profile.Collection, ref.ID, vector, payload); err != nil {
		return err
	}
	if len(jpeg) > 0 {
		if err := b.app.saveThumbnail(ref.ID, jpeg); err != nil {
			return err
		}
	}
	return b.app.store.put(file.ctx, "job", b.job.ID, b.job)
}

func (b *indexBatch) commit(file *fileIndexPlan) error {
	file.asset.Points = file.refs
	after, err := os.Stat(file.file.Path)
	if err != nil {
		return err
	}
	if after.Size() != file.original.Size() || !after.ModTime().Equal(file.original.ModTime()) {
		return errors.New("media changed during indexing")
	}
	if err := b.app.checkOriginal(file.asset, file.source); err != nil {
		return err
	}
	var cleanup *cleanupTask
	if file.previous != nil && file.previous.Revision != file.asset.Revision {
		task := cleanupFor(*file.previous, b.profile.Settings)
		cleanup = &task
	}
	if err := b.app.commitAsset(file.ctx, file.asset, cleanup); err != nil {
		return err
	}
	b.job.Indexed++
	updateCounters(file.ctx, b.job)
	emit(file.ctx, slog.LevelDebug, "file.processed", slog.String("result", "indexed"), slog.Int64("duration_ms", time.Since(file.started).Milliseconds()))
	progressed(file.ctx)
	b.release(file)
	return nil
}

func (b *indexBatch) fail(file *fileIndexPlan, err error) {
	if file.done {
		return
	}
	b.activate(file)
	b.report.issue(file.ctx, file.file.Location, "index_failed", atStage(file.ctx, err))
	b.release(file)
}

func (b *indexBatch) release(file *fileIndexPlan) {
	file.done = true
	delete(b.files, file)
	if file.framesDir != "" {
		_ = os.RemoveAll(file.framesDir)
	}
}

func (b *indexBatch) close() {
	for file := range b.files {
		b.release(file)
	}
}
