package backend

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

type App struct {
	store    *Store
	dir      string
	mu       sync.Mutex
	settings Settings
	ctx      context.Context
	cancel   context.CancelFunc
	wake     chan struct{}
	running  map[string]context.CancelFunc
	wg       sync.WaitGroup
}
type cleanupTask struct {
	ID         string   `json:"id"`
	Collection string   `json:"collection"`
	Points     []string `json:"points"`
	Settings   Settings `json:"settings"`
	Error      string   `json:"error,omitempty"`
}

func New(ctx context.Context, dir string) (*App, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	store, err := openStore(dir)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	a := &App{store: store, dir: dir, settings: DefaultSettings(), ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1), running: map[string]context.CancelFunc{}}
	fail := func(err error) (*App, error) { cancel(); _ = store.db.Close(); return nil, err }
	if err := store.get(ctx, "settings", "current", &a.settings); errors.Is(err, sql.ErrNoRows) {
		a.settings = DefaultSettings()
		if err := store.put(ctx, "settings", "current", a.settings); err != nil {
			return fail(err)
		}
	} else if err != nil {
		return fail(err)
	}
	for _, sub := range []string{"tmp", "thumbnails"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0700); err != nil {
			return fail(err)
		}
	}
	jobs, err := listRecords[Job](ctx, store, "job")
	if err != nil {
		return fail(err)
	}
	for _, job := range jobs {
		if job.Status == "running" {
			job.Status = "queued"
			job.Error = "resuming interrupted scan"
			if err := store.put(ctx, "job", job.ID, job); err != nil {
				return fail(err)
			}
		}
	}
	a.wg.Add(1)
	go a.worker()
	a.signal()
	return a, nil
}
func (a *App) Close() error { a.cancel(); a.wg.Wait(); return a.store.db.Close() }
func (a *App) signal() {
	select {
	case a.wake <- struct{}{}:
	default:
	}
}
func (a *App) Settings() Settings { a.mu.Lock(); defer a.mu.Unlock(); return a.settings }
func (a *App) SaveSettings(ctx context.Context, s Settings) error {
	if _, err := resolve(s); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.store.put(ctx, "settings", "current", s); err != nil {
		return err
	}
	a.settings = s
	return nil
}
func (a *App) Sources(ctx context.Context) ([]Source, error) {
	all, err := listRecords[Source](ctx, a.store, "source")
	if err != nil {
		return nil, err
	}
	result := []Source{}
	for _, s := range all {
		if s.Active {
			result = append(result, s)
		}
	}
	return result, nil
}
func (a *App) AddSource(ctx context.Context, root string) (Source, error) {
	if root == "" {
		return Source{}, errors.New("root_path is required")
	}
	canonical, err := filepath.Abs(root)
	if err != nil {
		return Source{}, err
	}
	canonical, err = realPath(canonical)
	if err != nil {
		return Source{}, err
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return Source{}, err
	}
	if !info.IsDir() {
		return Source{}, errors.New("source must be a directory")
	}
	// Excluding any overlap also prevents indexing the app's own expanding caches.
	if inside(canonical, a.dir) || inside(a.dir, canonical) {
		return Source{}, errors.New("source and app data directory must not overlap")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	sources, err := a.Sources(ctx)
	if err != nil {
		return Source{}, err
	}
	for _, s := range sources {
		other, err := os.Stat(s.RootPath)
		if err == nil && os.SameFile(info, other) {
			return Source{}, errors.New("source is already registered")
		}
	}
	source := Source{ID: uuid.NewString(), RootPath: canonical, Active: true, CreatedAt: time.Now().UTC()}
	return source, a.store.put(ctx, "source", source.ID, source)
}
func (a *App) RemoveSource(ctx context.Context, id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	var source Source
	if err := a.store.get(ctx, "source", id, &source); err != nil {
		return err
	}
	source.Active = false
	if err := a.store.put(ctx, "source", id, source); err != nil {
		return err
	}
	jobs, err := listRecords[Job](ctx, a.store, "job")
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if job.SourceID != id {
			continue
		}
		if cancel := a.running[job.ID]; cancel != nil {
			cancel()
		}
		if job.Status == "queued" {
			job.Status = "canceled"
			now := time.Now().UTC()
			job.FinishedAt = &now
			if err := a.store.put(ctx, "job", job.ID, job); err != nil {
				return err
			}
		}
	}
	assets, err := listRecords[Asset](ctx, a.store, "asset")
	if err != nil {
		return err
	}
	for _, asset := range assets {
		if asset.SourceID == id {
			if err := a.dropAsset(ctx, asset); err != nil {
				return err
			}
		}
	}
	a.signal()
	return nil
}
func (a *App) StartJob(ctx context.Context, sourceID string, force bool) (Job, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var source Source
	if err := a.store.get(ctx, "source", sourceID, &source); err != nil {
		return Job{}, err
	}
	if !source.Active {
		return Job{}, errors.New("source is removed")
	}
	if _, err := resolve(a.settings); err != nil {
		return Job{}, err
	}
	jobs, err := listRecords[Job](ctx, a.store, "job")
	if err != nil {
		return Job{}, err
	}
	for _, j := range jobs {
		if j.SourceID == sourceID && (j.Status == "queued" || j.Status == "running") {
			return Job{}, errors.New("source already has an active job")
		}
	}
	job := Job{ID: uuid.NewString(), SourceID: sourceID, Status: "queued", Force: force, Settings: a.settings, CreatedAt: time.Now().UTC(), Issues: []JobIssue{}}
	if err := a.store.put(ctx, "job", job.ID, job); err != nil {
		return Job{}, err
	}
	a.signal()
	return job, nil
}
func (a *App) CancelJob(ctx context.Context, id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	var job Job
	if err := a.store.get(ctx, "job", id, &job); err != nil {
		return err
	}
	if cancel := a.running[id]; cancel != nil {
		cancel()
		return nil
	}
	if job.Status == "queued" {
		job.Status = "canceled"
		now := time.Now().UTC()
		job.FinishedAt = &now
		return a.store.put(ctx, "job", id, job)
	}
	return nil
}
func (a *App) worker() {
	defer a.wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-a.wake:
		case <-ticker.C:
		}
		jobs, err := listRecords[Job](a.ctx, a.store, "job")
		if err != nil {
			continue
		}
		sort.Slice(jobs, func(i, j int) bool { return jobs[i].CreatedAt.Before(jobs[j].CreatedAt) })
		for _, job := range jobs {
			if a.ctx.Err() != nil {
				return
			}
			if job.Status == "queued" {
				a.runJob(job)
			}
		}
		cleanupCtx, cancel := context.WithTimeout(a.ctx, 20*time.Second)
		a.cleanup(cleanupCtx)
		cancel()
	}
}
func (a *App) runJob(job Job) {
	// A scan has no overall deadline; individual remote calls and media processes do.
	ctx, cancel := context.WithCancel(a.ctx)
	a.mu.Lock()
	// A queued job may have been canceled after the worker's initial snapshot.
	var current Job
	if err := a.store.get(ctx, "job", job.ID, &current); err != nil || current.Status != "queued" {
		a.mu.Unlock()
		cancel()
		return
	}
	job = current
	job.Status = "running"
	job.Error = ""
	now := time.Now().UTC()
	job.StartedAt = &now
	job.Discovered = 0
	job.Indexed = 0
	job.Skipped = 0
	job.Embedded = 0
	job.Cycles = 0
	job.IssueCount = 0
	job.Issues = []JobIssue{}
	a.running[job.ID] = cancel
	startErr := a.store.put(ctx, "job", job.ID, job)
	a.mu.Unlock()
	var err error
	if startErr != nil {
		err = startErr
	} else {
		err = a.indexSource(ctx, &job)
	}
	a.mu.Lock()
	delete(a.running, job.ID)
	a.mu.Unlock()
	// A shutdown leaves running jobs resumable; user cancellation is terminal.
	if a.ctx.Err() != nil {
		cancel()
		return
	}
	if ctx.Err() != nil {
		job.Status = "canceled"
		job.Error = ctx.Err().Error()
	} else if err != nil {
		job.Status = "failed"
		job.Error = err.Error()
	} else {
		job.Status = "succeeded"
	}
	now = time.Now().UTC()
	job.FinishedAt = &now
	saveCtx, saveCancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer saveCancel()
	_ = a.store.put(saveCtx, "job", job.ID, job)
	cancel()
}
func (a *App) dropAsset(ctx context.Context, asset Asset) error {
	var profile Profile
	if err := a.store.get(ctx, "profile", asset.ProfileID, &profile); err != nil {
		return err
	}
	task := cleanupFor(asset, profile.Settings)
	return a.store.changeAsset(ctx, asset, true, &task)
}
func cleanupFor(asset Asset, s Settings) cleanupTask {
	ids := []string{}
	for _, p := range asset.Points {
		ids = append(ids, p.ID)
	}
	return cleanupTask{ID: uuid.NewString(), Collection: asset.Collection, Points: ids, Settings: s}
}
func (a *App) cleanup(ctx context.Context) {
	tasks, err := listRecords[cleanupTask](ctx, a.store, "cleanup")
	if err != nil {
		return
	}
	for _, task := range tasks {
		cfg, err := resolve(task.Settings)
		if err == nil {
			err = newRemote(cfg).deletePoints(ctx, task.Collection, task.Points)
		}
		if err != nil {
			task.Error = err.Error()
			_ = a.store.put(ctx, "cleanup", task.ID, task)
			continue
		}
		for _, id := range task.Points {
			_ = os.Remove(filepath.Join(a.dir, "thumbnails", id+".jpg"))
		}
		_ = a.store.delete(ctx, "cleanup", task.ID)
	}
}
func stableID(parts ...any) string {
	b, _ := json.Marshal(parts)
	return uuid.NewSHA1(uuid.NameSpaceURL, b).String()
}

// Resolve asset IDs through the active catalog before any filesystem access.
func (a *App) asset(ctx context.Context, id string) (Asset, Source, error) {
	var asset Asset
	var source Source
	if err := a.store.get(ctx, "asset", id, &asset); err != nil {
		return asset, source, err
	}
	if err := a.store.get(ctx, "source", asset.SourceID, &source); err != nil {
		return asset, source, err
	}
	if !source.Active {
		return asset, source, sql.ErrNoRows
	}
	return asset, source, nil
}
func (a *App) checkOriginal(asset Asset, source Source) error {
	root, err := realPath(source.RootPath)
	if err != nil {
		return err
	}
	file, err := sourcePath(root, asset.Location.RelativePath)
	if err != nil {
		return err
	}
	if !inside(root, file) {
		return errors.New("path is outside source")
	}
	info, err := os.Stat(file)
	if err != nil {
		return err
	}
	if info.Size() != asset.OuterSize || info.ModTime().UnixNano() != asset.OuterModTime {
		return errors.New("original has changed; reindex this source")
	}
	return nil
}
