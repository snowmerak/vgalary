package backend

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Option func(*App)

// WithLogger installs the process-owned logger before background work starts.
func WithLogger(logger *slog.Logger) Option {
	return func(a *App) {
		if logger != nil {
			a.logger = logger
		}
	}
}

type observationKey struct{}
type execution struct {
	logger      *slog.Logger
	observation *observation
}
type suppression struct{ count, pending int }
type observation struct {
	mu                       sync.Mutex
	logger                   *slog.Logger
	stage                    string
	stageSince, lastProgress time.Time
	lastFlush                time.Time
	item                     string
	asset                    string
	profile                  string
	counters                 []slog.Attr
	seen                     map[string]*suppression
	suppressed               int
}

func observe(ctx context.Context, logger *slog.Logger) context.Context {
	now := time.Now()
	o := &observation{logger: logger, stage: "initializing", stageSince: now, lastProgress: now, lastFlush: now, seen: map[string]*suppression{}}
	return context.WithValue(ctx, observationKey{}, execution{logger, o})
}
func (a *App) logContext(ctx context.Context) context.Context {
	if executionOf(ctx).logger != nil {
		return ctx
	}
	return observe(ctx, a.logger)
}
func executionOf(ctx context.Context) execution {
	e, _ := ctx.Value(observationKey{}).(execution)
	return e
}
func bindLog(ctx context.Context, attrs ...slog.Attr) context.Context {
	e := executionOf(ctx)
	if e.logger == nil {
		return ctx
	}
	args := make([]any, len(attrs))
	for i, a := range attrs {
		args[i] = a
	}
	e.logger = e.logger.With(args...)
	return context.WithValue(ctx, observationKey{}, e)
}
func setStage(ctx context.Context, stage string) {
	if o := executionOf(ctx).observation; o != nil {
		o.mu.Lock()
		o.stage = stage
		o.stageSince = time.Now()
		o.mu.Unlock()
	}
}
func progressed(ctx context.Context) {
	if o := executionOf(ctx).observation; o != nil {
		o.mu.Lock()
		o.lastProgress = time.Now()
		o.mu.Unlock()
	}
}
func itemFields(ctx context.Context) []slog.Attr {
	if o := executionOf(ctx).observation; o != nil {
		o.mu.Lock()
		defer o.mu.Unlock()
		return []slog.Attr{slog.String("item_id", o.item), slog.String("asset_id", o.asset), slog.String("stage", o.stage)}
	}
	return nil
}
func updateCounters(ctx context.Context, job *Job) {
	if o := executionOf(ctx).observation; o != nil {
		o.mu.Lock()
		o.counters = jobCounts(job)
		o.mu.Unlock()
	}
}
func jobCounts(job *Job) []slog.Attr {
	return []slog.Attr{slog.Int("discovered", job.Discovered), slog.Int("indexed", job.Indexed), slog.Int("reused", job.Skipped), slog.Int("embedding_calls", job.EmbeddingRequests), slog.Int("embedded_images", job.Embedded), slog.Int("cycles", job.Cycles), slog.Int("issue_count", job.IssueCount)}
}

func emit(ctx context.Context, level slog.Level, event string, attrs ...slog.Attr) {
	e := executionOf(ctx)
	if e.logger == nil || !e.logger.Enabled(ctx, level) {
		return
	}
	if o := e.observation; o != nil && (event == "file.failed" || event == "dependency.retry" || event == "cleanup.failed" || event == "worker.poll_failed") {
		key := event
		for _, a := range attrs {
			if a.Key == "error_code" || a.Key == "stage" || a.Key == "dependency" || a.Key == "cleanup_id" {
				key += "/" + a.Value.String()
			}
		}
		o.mu.Lock()
		if _, ok := o.seen[key]; !ok {
			if len(o.seen) >= 63 {
				key = "other"
			}
			if o.seen[key] == nil {
				o.seen[key] = &suppression{}
			}
		}
		s := o.seen[key]
		s.count++
		allowed := s.count <= 5
		if event == "worker.poll_failed" || event == "cleanup.failed" {
			allowed = s.count <= 1
		}
		if !allowed {
			s.pending++
			o.suppressed++
		}
		o.mu.Unlock()
		if !allowed {
			return
		}
	}
	attrs = append([]slog.Attr{slog.String("event", event)}, attrs...)
	e.logger.LogAttrs(ctx, level, event, attrs...)
}
func (o *observation) flush() {
	o.flushAt(time.Now(), false)
}
func (o *observation) finish() { o.flushAt(time.Now(), true) }
func (o *observation) flushAt(now time.Time, force bool) {
	o.mu.Lock()
	if !force && now.Sub(o.lastFlush) < 30*time.Second {
		o.mu.Unlock()
		return
	}
	o.lastFlush = now
	pending := 0
	for _, s := range o.seen {
		pending += s.pending
		s.pending = 0
	}
	total := o.suppressed
	o.mu.Unlock()
	if pending > 0 {
		o.logger.Warn("repeated diagnostics suppressed", "event", "logging.suppressed", "suppressed_count", pending, "suppressed_total", total)
	}
}
func reportProgress(ctx context.Context) {
	e := executionOf(ctx)
	o := e.observation
	if o == nil {
		return
	}
	now := time.Now()
	o.mu.Lock()
	attrs := append([]slog.Attr(nil), o.counters...)
	attrs = append(attrs, slog.String("stage", o.stage), slog.Int64("stage_elapsed_ms", now.Sub(o.stageSince).Milliseconds()), slog.Int64("last_progress_age_ms", now.Sub(o.lastProgress).Milliseconds()))
	if o.item != "" {
		attrs = append(attrs, slog.String("item_id", o.item))
	}
	if o.profile != "" {
		attrs = append(attrs, slog.String("profile_id", o.profile))
	}
	o.mu.Unlock()
	emit(ctx, slog.LevelInfo, "job.progress", attrs...)
	o.flush()
}
func startProgress(ctx context.Context) func() {
	stop, done := make(chan struct{}), make(chan struct{})
	var once sync.Once
	go func() {
		defer close(done)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				reportProgress(ctx)
			}
		}
	}()
	return func() {
		once.Do(func() {
			close(stop)
			<-done
			if o := executionOf(ctx).observation; o != nil {
				o.finish()
			}
		})
	}
}

// failure retains causes for classification while Error preserves existing API
// messages. Raw causes are never passed to the diagnostic logger.
type failure struct {
	err, cause              error
	code, stage, dependency string
	attrs                   []slog.Attr
}

func (e *failure) Error() string { return e.err.Error() }
func (e *failure) Unwrap() error {
	if e.cause != nil {
		return e.cause
	}
	return e.err
}
func fail(err, errorCause error, code, stage, dependency string, attrs ...slog.Attr) error {
	return &failure{err: err, cause: errorCause, code: code, stage: stage, dependency: dependency, attrs: attrs}
}
func failureFields(err error) []slog.Attr {
	if err == nil {
		return nil
	}
	var f *failure
	if errors.As(err, &f) {
		attrs := append([]slog.Attr{slog.String("error_code", f.code), slog.String("stage", f.stage), slog.String("dependency", f.dependency)}, f.attrs...)
		return attrs
	}
	code := "operation_failed"
	var network net.Error
	switch {
	case errors.Is(err, context.Canceled):
		code = "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		code = "deadline_exceeded"
	case errors.Is(err, fs.ErrPermission):
		code = "permission_denied"
	case errors.Is(err, fs.ErrNotExist):
		code = "not_found"
	case errors.As(err, &network):
		if network.Timeout() {
			code = "request_timeout"
		} else {
			code = "network_error"
		}
	}
	return []slog.Attr{slog.String("error_code", code)}
}
func atStage(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	var f *failure
	if errors.As(err, &f) {
		return err
	}
	stage := "unknown"
	if o := executionOf(ctx).observation; o != nil {
		o.mu.Lock()
		stage = o.stage
		o.mu.Unlock()
	}
	fields := failureFields(err)
	return fail(err, nil, fields[0].Value.String(), stage, "")
}

var errUserCanceled = errors.New("user_canceled")
var errSourceRemoved = errors.New("source_removed")

func cancelReason(ctx context.Context) string {
	switch {
	case errors.Is(context.Cause(ctx), errUserCanceled):
		return "user_canceled"
	case errors.Is(context.Cause(ctx), errSourceRemoved):
		return "source_removed"
	default:
		return "request_canceled"
	}
}
func operationResult(err error) string {
	if err == nil {
		return "succeeded"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	return "failed"
}

type responseStatus struct {
	http.ResponseWriter
	status  int
	failure []slog.Attr
}

func (w *responseStatus) diagnosticFailure(err error) { w.failure = failureFields(err) }

func (w *responseStatus) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *responseStatus) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}
func (w *responseStatus) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (a *App) logHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := uuid.NewString()
		ctx := observe(r.Context(), a.logger.With("request_id", id))
		defer executionOf(ctx).observation.finish()
		w.Header().Set("X-Request-ID", id)
		status := &responseStatus{ResponseWriter: w}
		request := r.WithContext(ctx)
		next.ServeHTTP(status, request)
		code := status.status
		if code == 0 {
			code = 200
		}
		level, event := slog.LevelDebug, "http.finished"
		if code >= 500 {
			level, event = slog.LevelError, "http.failed"
		}
		// Pattern is a registered route template, never a raw URL or query.
		route := request.Pattern
		if route == "" {
			route = "unmatched"
		}
		method := r.Method
		switch method {
		case "GET", "HEAD", "POST", "PUT", "DELETE", "OPTIONS", "PATCH", "CONNECT", "TRACE":
		default:
			method = "OTHER"
		}
		attrs := []slog.Attr{slog.String("method", method), slog.String("route", route), slog.Int("http_status", code), slog.Int64("duration_ms", time.Since(start).Milliseconds())}
		if code >= 500 {
			if len(status.failure) > 0 {
				attrs = append(attrs, status.failure...)
			} else {
				attrs = append(attrs, slog.String("error_code", "http_server_error"))
			}
		}
		emit(ctx, level, event, attrs...)
	})
}
