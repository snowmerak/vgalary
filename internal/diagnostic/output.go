// Package diagnostic provides bounded, best-effort local diagnostic output.
package diagnostic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const maxRecordBytes = 4096

type Options struct {
	Level  string
	Format string
	Paths  bool
}

type record struct {
	data  []byte
	level int
}

// Output owns one writer goroutine. Close bounds the wait, not the underlying
// writer: a blocked OS write can only be released by that writer or process exit.
type Output struct {
	Logger   *slog.Logger
	writer   io.Writer
	queue    chan record
	done     chan struct{}
	mu       sync.Mutex
	closed   bool
	dropped  [4]atomic.Uint64
	options  Options
	instance string
}

func New(writer io.Writer, options Options) (*Output, error) {
	var level slog.Level
	if options.Level == "" {
		options.Level = "info"
	}
	switch options.Level {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		return nil, errors.New("invalid log level")
	}
	if options.Format == "" {
		options.Format = "json"
	}
	if options.Format != "json" && options.Format != "text" {
		return nil, errors.New("invalid log format")
	}
	if writer == nil {
		return nil, errors.New("log writer is required")
	}
	o := &Output{writer: writer, queue: make(chan record, 256), done: make(chan struct{}), options: options, instance: uuid.NewString()}
	o.Logger = slog.New(&handler{output: o, level: level})
	go o.write()
	return o, nil
}

func (o *Output) Close(ctx context.Context) error {
	o.mu.Lock()
	if !o.closed {
		o.closed = true
		close(o.queue)
	}
	o.mu.Unlock()
	select {
	case <-o.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func levelIndex(level slog.Level) int {
	if level >= slog.LevelError {
		return 3
	}
	if level >= slog.LevelWarn {
		return 2
	}
	if level >= slog.LevelInfo {
		return 1
	}
	return 0
}

func (o *Output) write() {
	defer close(o.done)
	for r := range o.queue {
		if !o.writeRecord(r.data) {
			o.dropped[r.level].Add(1)
			continue
		}
		o.reportDrops()
	}
	o.reportDrops()
}

func (o *Output) writeRecord(data []byte) bool {
	n, err := o.writer.Write(data)
	return err == nil && n == len(data)
}

func (o *Output) reportDrops() {
	counts := [4]uint64{}
	var total uint64
	for i := range counts {
		counts[i] = o.dropped[i].Swap(0)
		total += counts[i]
	}
	if total == 0 {
		return
	}
	level := "WARN"
	if o.options.Level == "error" {
		level = "ERROR"
	}
	data := o.encode(map[string]any{"time": time.Now().UTC().Format(time.RFC3339Nano), "level": level, "msg": "diagnostic records dropped", "event": "logging.dropped", "instance_id": o.instance, "dropped_count": total, "debug_dropped": counts[0], "info_dropped": counts[1], "warn_dropped": counts[2], "error_dropped": counts[3]})
	if !o.writeRecord(data) {
		for i, n := range counts {
			o.dropped[i].Add(n)
		}
	}
}

func (o *Output) encode(fields map[string]any) []byte {
	data, _ := json.Marshal(fields)
	budget := maxRecordBytes - 128
	if len(data) > budget {
		// Keep identity and the event when many individually bounded fields exceed
		// the record budget. No oversized raw record is ever submitted to the queue.
		for key := range fields {
			switch key {
			case "time", "level", "msg", "event", "instance_id", "job_id", "run_id", "request_id", "error_code":
			default:
				delete(fields, key)
			}
		}
		fields["truncated"] = true
		data, _ = json.Marshal(fields)
	}
	if len(data) > budget {
		for key, value := range fields {
			if text, ok := value.(string); ok {
				fields[key] = clip(text, 48)
			}
		}
		data, _ = json.Marshal(fields)
	}
	if o.options.Format == "text" {
		// JSON-quoted values preserve one physical line, including control chars.
		return []byte(fmt.Sprintf("%s %s %s\n", fields["time"], fields["level"], data))
	}
	return append(data, '\n')
}

type handler struct {
	output *Output
	level  slog.Level
	attrs  []slog.Attr
	groups []string
}

func (h *handler) Enabled(_ context.Context, level slog.Level) bool { return level >= h.level }
func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	// Store grouping at the point of binding, following slog Handler semantics.
	bound := append([]slog.Attr(nil), attrs...)
	for i := len(h.groups) - 1; i >= 0; i-- {
		bound = []slog.Attr{slog.GroupAttrs(h.groups[i], bound...)}
	}
	clone.attrs = append(append([]slog.Attr(nil), h.attrs...), bound...)
	return &clone
}
func (h *handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	clone := *h
	clone.groups = append(append([]string(nil), h.groups...), name)
	return &clone
}

// The allowlist intentionally rejects arbitrary objects, errors, URLs and bodies.
// New event fields must be reviewed here as well as at their domain owner.
const allowed = " event job_id source_id profile_id run_id request_id operation_id asset_id item_id cleanup_id stage operation dependency result reason error_code duration_ms stage_elapsed_ms last_progress_age_ms retry_delay_ms attempt attempts http_status exit_code discovered indexed reused embedding_calls cycles issue_count candidates selected threshold selection_kind archive_depth relative_path method route suppressed_count suppressed_total pending_count from_state to_state truncated listen_address "

func (h *handler) Handle(_ context.Context, r slog.Record) error {
	fields := map[string]any{"time": r.Time.UTC().Format(time.RFC3339Nano), "level": r.Level.String(), "msg": clip(r.Message, 512), "instance_id": h.output.instance}
	var add func(map[string]any, slog.Attr, int)
	add = func(target map[string]any, a slog.Attr, depth int) {
		if depth > 3 {
			return
		}
		// Do not call arbitrary LogValuer/String methods on untrusted values.
		if a.Value.Kind() == slog.KindGroup {
			if a.Key == "" {
				for _, child := range a.Value.Group() {
					add(target, child, depth+1)
				}
				return
			}
			// Only an explicitly approved grouping can contain approved fields.
			if a.Key != "diagnostic" {
				return
			}
			group, _ := target[a.Key].(map[string]any)
			if group == nil {
				group = map[string]any{}
			}
			for _, child := range a.Value.Group() {
				add(group, child, depth+1)
			}
			target[a.Key] = group
			return
		}
		if !strings.Contains(allowed, " "+a.Key+" ") {
			return
		}
		switch a.Value.Kind() {
		case slog.KindString:
			value := a.Value.String()
			if a.Key == "relative_path" && (!h.output.options.Paths || !safeRelative(value)) {
				return
			}
			if len(value) > 512 {
				fields["truncated"] = true
			}
			target[a.Key] = clip(value, 512)
		case slog.KindInt64:
			target[a.Key] = a.Value.Int64()
		case slog.KindUint64:
			target[a.Key] = a.Value.Uint64()
		case slog.KindBool:
			target[a.Key] = a.Value.Bool()
		case slog.KindFloat64:
			value := a.Value.Float64()
			if !math.IsNaN(value) && !math.IsInf(value, 0) {
				target[a.Key] = value
			}
		}
	}
	for _, a := range h.attrs {
		add(fields, a, 0)
	}
	attrs := []slog.Attr{}
	r.Attrs(func(a slog.Attr) bool { attrs = append(attrs, a); return len(attrs) < 32 })
	for i := len(h.groups) - 1; i >= 0; i-- {
		attrs = []slog.Attr{slog.GroupAttrs(h.groups[i], attrs...)}
	}
	for _, a := range attrs {
		add(fields, a, 0)
	}
	data := h.output.encode(fields)
	h.output.mu.Lock()
	defer h.output.mu.Unlock()
	if h.output.closed {
		return nil
	}
	select {
	case h.output.queue <- record{data, levelIndex(r.Level)}:
	default:
		h.output.dropped[levelIndex(r.Level)].Add(1)
	}
	return nil
}

func clip(value string, limit int) string {
	value = strings.ToValidUTF8(value, "�")
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
func safeRelative(value string) bool {
	value = strings.ReplaceAll(value, "\\", "/")
	if filepath.IsAbs(value) || strings.HasPrefix(value, "/") || strings.Contains(value, ":") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}
