package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/snowmerak/vgalary/internal/diagnostic"
)

type capturedLogs struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (c *capturedLogs) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.data.Write(p)
}
func (c *capturedLogs) records(t *testing.T) []map[string]any {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	var all []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(c.data.Bytes()), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var r map[string]any
		if err := json.Unmarshal(line, &r); err != nil {
			t.Fatal(err)
		}
		all = append(all, r)
	}
	return all
}
func (c *capturedLogs) wait(t *testing.T, event, jobID string) map[string]any {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		for _, r := range c.records(t) {
			if r["event"] == event && (jobID == "" || r["job_id"] == jobID) {
				return r
			}
		}
		select {
		case <-deadline.C:
			t.Fatalf("missing %s for %s: %+v", event, jobID, c.records(t))
		case <-tick.C:
		}
	}
}
func testLogger(t *testing.T, level string) (*slog.Logger, *capturedLogs) {
	t.Helper()
	sink := &capturedLogs{}
	out, err := diagnostic.New(sink, diagnostic.Options{Level: level})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := out.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return out.Logger, sink
}
func loggedApp(t *testing.T, f *fakeVectors, logger *slog.Logger) *App {
	t.Helper()
	a, err := New(context.Background(), t.TempDir(), WithLogger(logger))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	settings := a.Settings()
	settings.Embedding.BaseURL = f.server.URL + "/v1"
	settings.Qdrant.URL = f.server.URL
	if err := a.SaveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	return a
}
func loggedSource(t *testing.T, a *App) Source {
	t.Helper()
	root := t.TempDir()
	writeTestImage(t, filepath.Join(root, "SECRET-filename.jpg"))
	source, err := a.AddSource(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func TestJobLogsMatchDurableResultAndDoNotExposeMedia(t *testing.T) {
	logger, logs := testLogger(t, "info")
	f := newFakeVectors(t)
	a := loggedApp(t, f, logger)
	source := loggedSource(t, a)
	job := scanTestSource(t, a, source)
	if job.Status != "succeeded" {
		t.Fatal(job.Error)
	}
	finished := logs.wait(t, "job.finished", job.ID)
	if record, err := json.Marshal(finished); err == nil {
		t.Logf("captured runtime event: %s", record)
	}
	started := logs.wait(t, "job.started", job.ID)
	if finished["run_id"] != started["run_id"] || finished["run_id"] == "" || finished["result"] != job.Status || finished["indexed"] != float64(job.Indexed) {
		t.Fatalf("log/state mismatch: %+v %+v", started, finished)
	}
	assets, err := listRecords[Asset](context.Background(), a.store, "asset")
	if err != nil || len(assets) != 1 {
		t.Fatal("missing indexed media")
	}
	request := httptest.NewRequest("GET", "http://localhost/api/assets/"+assets[0].ID+"/content", nil)
	request.Header.Set("Range", "bytes=0-9")
	response := httptest.NewRecorder()
	a.Handler().ServeHTTP(response, request)
	if response.Code != 206 || response.Body.Len() != 10 {
		t.Fatalf("response wrapper broke Range downloads: %d / %d", response.Code, response.Body.Len())
	}
	second := scanTestSource(t, a, source)
	reused := logs.wait(t, "job.finished", second.ID)
	if reused["reused"] != float64(1) {
		t.Fatal("reuse count missing")
	}
	for _, r := range logs.records(t) {
		encoded, _ := json.Marshal(r)
		if strings.Contains(string(encoded), "SECRET-filename") || strings.Contains(string(encoded), source.RootPath) {
			t.Fatal("media path leaked")
		}
		if r["event"] == "file.processed" || r["event"] == "dependency.finished" {
			t.Fatal("normal file/call noise at info")
		}
	}
}

func TestJobCancelRemovalAndResumeDiagnostics(t *testing.T) {
	for _, action := range []string{"user_canceled", "source_removed", "server_shutdown"} {
		t.Run(action, func(t *testing.T) {
			logger, logs := testLogger(t, "info")
			f := newFakeVectors(t)
			f.blockImage.Store(true)
			a := loggedApp(t, f, logger)
			source := loggedSource(t, a)
			job, err := a.StartJob(context.Background(), source.ID, false)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-f.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("embedding did not start")
			}
			switch action {
			case "user_canceled":
				err = a.CancelJob(context.Background(), job.ID)
			case "source_removed":
				err = a.RemoveSource(context.Background(), source.ID)
			case "server_shutdown":
				err = a.Close()
			}
			if err != nil {
				t.Fatal(err)
			}
			if action == "server_shutdown" {
				interrupted := logs.wait(t, "job.interrupted", job.ID)
				if interrupted["reason"] != action {
					t.Fatal(interrupted)
				}
				f.blockImage.Store(false)
				resumed, err := New(context.Background(), a.dir, WithLogger(logger))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = resumed.Close() })
				finished := awaitJob(t, resumed, job.ID)
				if finished.Status != "succeeded" {
					t.Fatal(finished.Error)
				}
				restart := logs.wait(t, "job.resumed", job.ID)
				if restart["run_id"] == interrupted["run_id"] {
					t.Fatal("resumed execution reused run ID")
				}
				logs.wait(t, "job.finished", job.ID)
			} else {
				finished := awaitJob(t, a, job.ID)
				if finished.Status != "canceled" {
					t.Fatal(finished)
				}
				record := logs.wait(t, "job.finished", job.ID)
				if record["reason"] != action || record["level"] != "INFO" {
					t.Fatal(record)
				}
			}
		})
	}
}

func TestQueuedCancelHasNoExecutionID(t *testing.T) {
	logger, logs := testLogger(t, "info")
	f := newFakeVectors(t)
	f.blockImage.Store(true)
	a := loggedApp(t, f, logger)
	first := loggedSource(t, a)
	_, err := a.StartJob(context.Background(), first.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first job did not block")
	}
	second := loggedSource(t, a)
	job, err := a.StartJob(context.Background(), second.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.CancelJob(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	r := logs.wait(t, "job.finished", job.ID)
	if _, ok := r["run_id"]; ok {
		t.Fatal("queued cancellation invented an execution")
	}
}

func TestFinalJobSaveFailureDoesNotClaimSuccess(t *testing.T) {
	logger, logs := testLogger(t, "info")
	f := newFakeVectors(t)
	a := loggedApp(t, f, logger)
	source := loggedSource(t, a)
	_, err := a.store.db.Exec(`CREATE TRIGGER fail_final_job BEFORE UPDATE ON records WHEN NEW.kind='job' AND json_extract(CAST(NEW.body AS TEXT),'$.status') IN ('succeeded','failed','canceled') BEGIN SELECT RAISE(FAIL,'SECRET-storage-message'); END;`)
	if err != nil {
		t.Fatal(err)
	}
	job, err := a.StartJob(context.Background(), source.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	record := logs.wait(t, "job.persist_failed", job.ID)
	if record["stage"] != "finish" || record["level"] != "ERROR" {
		t.Fatal(record)
	}
	var stored Job
	if err := a.store.get(context.Background(), "job", job.ID, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Status != "running" {
		t.Fatalf("unexpected persisted state: %s", stored.Status)
	}
	for _, r := range logs.records(t) {
		if r["event"] == "job.finished" {
			t.Fatal("failed final save reported completion")
		}
		encoded, _ := json.Marshal(r)
		if strings.Contains(string(encoded), "SECRET-storage-message") {
			t.Fatal("raw storage error leaked")
		}
	}
}

func TestDependencyRetryCorrelationAndSafeFields(t *testing.T) {
	logger, logs := testLogger(t, "debug")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(503)
			_, _ = w.Write([]byte("SECRET-response-body"))
			return
		}
		writeJSON(w, 200, map[string]any{"data": []any{map[string]any{"index": 0, "embedding": []float32{1, 2}}}})
	}))
	defer server.Close()
	cfg, _ := resolve(DefaultSettings())
	cfg.EmbeddingURL = server.URL
	cfg.EmbeddingKey = "SECRET-api-key"
	ctx := observe(context.Background(), logger.With("job_id", "job", "run_id", "run"))
	if _, err := newRemote(cfg).embedText(ctx, "SECRET-query"); err != nil {
		t.Fatal(err)
	}
	retry := logs.wait(t, "dependency.retry", "job")
	finished := logs.wait(t, "dependency.finished", "job")
	if retry["operation_id"] != finished["operation_id"] || retry["attempt"] != float64(1) || finished["attempts"] != float64(2) || retry["http_status"] != float64(503) || finished["result"] != "succeeded" {
		t.Fatalf("retry correlation mismatch: %+v %+v", retry, finished)
	}
	for _, r := range logs.records(t) {
		body, _ := json.Marshal(r)
		if strings.Contains(string(body), "SECRET-") {
			t.Fatal("external content leaked")
		}
	}
}

func TestRequestTimeoutIsDistinctAndPreservesAPIMessage(t *testing.T) {
	logger, logs := testLogger(t, "debug")
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	cfg, _ := resolve(DefaultSettings())
	cfg.EmbeddingURL = server.URL
	c := newRemote(cfg)
	c.http.Timeout = 10 * time.Millisecond
	_, err := c.embedText(observe(context.Background(), logger), "SECRET-query")
	if err == nil || !strings.Contains(err.Error(), "remote connection failed") {
		t.Fatal("API error behavior changed")
	}
	r := logs.wait(t, "dependency.finished", "")
	if r["error_code"] != "request_timeout" || r["attempts"] != float64(3) {
		t.Fatalf("timeout not classified: %+v", r)
	}
}

func TestHTTPLogsUseTemplatesAndGeneratedIDs(t *testing.T) {
	logger, logs := testLogger(t, "debug")
	f := newFakeVectors(t)
	a := loggedApp(t, f, logger)
	source := loggedSource(t, a)
	if err := a.store.put(context.Background(), "asset", "corrupt", map[string]any{"id": "corrupt", "source_id": source.ID, "points": "invalid"}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "http://localhost/api/assets?query=SECRET-query", nil)
	req.Header.Set("X-Request-ID", "SECRET-untrusted-id")
	req.Header.Set("Authorization", "SECRET-auth")
	response := httptest.NewRecorder()
	a.Handler().ServeHTTP(response, req)
	if response.Code != 500 {
		t.Fatal(response.Code)
	}
	r := logs.wait(t, "http.failed", "")
	if r["route"] != "GET /api/assets" || r["request_id"] != response.Header().Get("X-Request-ID") || r["request_id"] == "SECRET-untrusted-id" {
		t.Fatalf("bad HTTP context: %+v", r)
	}
	for _, record := range logs.records(t) {
		data, _ := json.Marshal(record)
		if strings.Contains(string(data), "SECRET-") {
			t.Fatal("request payload or headers leaked")
		}
	}
}

func TestSuppressionProgressAndWorkerFailure(t *testing.T) {
	logger, logs := testLogger(t, "info")
	ctx := observe(context.Background(), logger.With("job_id", "job"))
	setStage(ctx, "embedding.image")
	updateCounters(ctx, &Job{Indexed: 7, Skipped: 2})
	for i := 0; i < 100; i++ {
		emit(ctx, slog.LevelWarn, "file.failed", slog.String("stage", "image.decode"), slog.String("error_code", "bad_image"))
	}
	o := executionOf(ctx).observation
	o.flush()
	for _, r := range logs.records(t) {
		if r["event"] == "logging.suppressed" {
			t.Fatal("suppression flushed before window")
		}
	}
	o.flushAt(time.Now().Add(31*time.Second), false)
	summary := logs.wait(t, "logging.suppressed", "job")
	if summary["suppressed_count"] != float64(95) {
		t.Fatal(summary)
	}
	reportProgress(ctx)
	progress := logs.wait(t, "job.progress", "job")
	if progress["stage"] != "embedding.image" || progress["indexed"] != float64(7) || progress["last_progress_age_ms"] == nil {
		t.Fatal(progress)
	}
	for i := 0; i < 100; i++ {
		emit(ctx, slog.LevelWarn, "file.failed", slog.String("error_code", strings.Repeat("x", i)))
	}
	if len(o.seen) > 64 {
		t.Fatal("suppression keys grew without bound")
	}
	stop := startProgress(ctx)
	stop()
	stop()
	f := newFakeVectors(t)
	a := loggedApp(t, f, logger)
	if err := a.store.db.Close(); err != nil {
		t.Fatal(err)
	}
	a.signal()
	logs.wait(t, "worker.poll_failed", "")
}

func TestCleanupFailureAndRecoveryEvents(t *testing.T) {
	logger, logs := testLogger(t, "info")
	f := newFakeVectors(t)
	store, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.db.Close() }()
	settings := DefaultSettings()
	settings.Qdrant.URL = f.server.URL
	task := cleanupTask{ID: "cleanup", Collection: "fixture", Points: []string{"point"}, Settings: settings}
	if err := store.put(context.Background(), "cleanup", task.ID, task); err != nil {
		t.Fatal(err)
	}
	a := &App{store: store, dir: t.TempDir(), logger: logger}
	ctx := observe(context.Background(), logger)
	f.unavailable.Store(true)
	a.cleanup(ctx)
	failed := logs.wait(t, "cleanup.failed", "")
	if failed["cleanup_id"] != task.ID {
		t.Fatal(failed)
	}
	f.unavailable.Store(false)
	a.cleanup(ctx)
	recovered := logs.wait(t, "cleanup.recovered", "")
	if recovered["pending_count"] != float64(0) {
		t.Fatal(recovered)
	}
}

func TestProgressTimerAndStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var sink bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&sink, nil))
		ctx := observe(context.Background(), logger)
		setStage(ctx, "archive.read")
		stop := startProgress(ctx)
		time.Sleep(29 * time.Second)
		synctest.Wait()
		if sink.Len() != 0 {
			t.Fatal("progress emitted before interval")
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if !strings.Contains(sink.String(), `"event":"job.progress"`) {
			t.Fatal("progress timer did not fire")
		}
		stop()
		size := sink.Len()
		time.Sleep(time.Minute)
		synctest.Wait()
		if sink.Len() != size {
			t.Fatal("progress reporter survived its owner")
		}
	})
}

func TestDiagnosticMediaHelper(t *testing.T) {
	switch os.Getenv("VGALARY_DIAGNOSTIC_MEDIA_HELPER") {
	case "exit":
		_, _ = os.Stderr.WriteString("SECRET-process-stderr")
		os.Exit(7)
	case "timeout":
		time.Sleep(10 * time.Second)
		os.Exit(0)
	}
}

func TestMediaFailureAndTimeoutLogs(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"exit", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("VGALARY_DIAGNOSTIC_MEDIA_HELPER", mode)
			logger, logs := testLogger(t, "debug")
			p := DefaultSettings().Processing
			p.FFmpeg = executable
			p.ProcessTimeoutSeconds = 1
			_, err := runMedia(observe(context.Background(), logger), p, executable, "-test.run=^TestDiagnosticMediaHelper$")
			if err == nil {
				t.Fatal("helper did not fail")
			}
			r := logs.wait(t, "media.finished", "")
			if mode == "timeout" && r["error_code"] != "process_timeout" {
				t.Fatal(r)
			}
			if mode == "exit" && (r["error_code"] != "media_process_failed" || r["exit_code"] != float64(7)) {
				t.Fatal(r)
			}
			data, _ := json.Marshal(r)
			if strings.Contains(string(data), "SECRET-") {
				t.Fatal("process stderr leaked")
			}
		})
	}
}

func TestProfileWaitingActivationAndIncompleteScanLogs(t *testing.T) {
	logger, logs := testLogger(t, "info")
	f := newFakeVectors(t)
	a := loggedApp(t, f, logger)
	first, second := loggedSource(t, a), loggedSource(t, a)
	for _, source := range []Source{first, second} {
		if job := scanTestSource(t, a, source); job.Status != "succeeded" {
			t.Fatal(job.Error)
		}
	}
	var oldProfile string
	if err := a.store.get(context.Background(), "meta", "active_profile", &oldProfile); err != nil {
		t.Fatal(err)
	}
	settings := a.Settings()
	settings.Embedding.ModelRevision = "next"
	if err := a.SaveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	job := scanTestSource(t, a, first)
	logs.wait(t, "profile.waiting", job.ID)
	var profile string
	if err := a.store.get(context.Background(), "meta", "active_profile", &profile); err != nil || profile != oldProfile {
		t.Fatal("pending profile activated prematurely")
	}
	job = scanTestSource(t, a, second)
	activated := logs.wait(t, "profile.activated", job.ID)
	if activated["profile_id"] == oldProfile {
		t.Fatal("wrong active profile logged")
	}
	if err := os.WriteFile(filepath.Join(first.RootPath, "broken.zip"), makeZIP(t, "../escape.jpg", []byte("fixture")), 0600); err != nil {
		t.Fatal(err)
	}
	job = scanTestSource(t, a, first)
	finished := logs.wait(t, "job.finished", job.ID)
	failed := logs.wait(t, "file.failed", job.ID)
	if job.Status != "failed" || finished["error_code"] != "scan_incomplete" || failed["stage"] != "archive.read" || failed["item_id"] == "" {
		t.Fatalf("incomplete scan diagnostics missing: %+v %+v", finished, failed)
	}
}
