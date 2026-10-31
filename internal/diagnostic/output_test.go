package diagnostic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

func closeOutput(t *testing.T, o *Output) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := o.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
func records(t *testing.T, b []byte) []map[string]any {
	t.Helper()
	var result []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(b), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		if len(line)+1 > maxRecordBytes {
			t.Fatalf("oversized record: %d", len(line))
		}
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("invalid JSON: %s: %v", line, err)
		}
		result = append(result, record)
	}
	return result
}

func TestOutputRedactsBoundsAndFilters(t *testing.T) {
	var sink bytes.Buffer
	o, err := New(&sink, Options{})
	if err != nil {
		t.Fatal(err)
	}
	secret := "SECRET-DO-NOT-LOG"
	o.Logger.Debug("debug hidden", "event", "file.processed")
	o.Logger.With("authorization", secret).Info("safe event", "event", "job.started", "job_id", "job-id", "error", errors.New(secret), "url", "https://"+secret, "query", secret, "body", secret, "relative_path", secret)
	o.Logger.Info("safe float", "event", "media.frames_selected", "threshold", math.NaN())
	closeOutput(t, o)
	if strings.Contains(sink.String(), secret) || strings.Contains(sink.String(), "debug hidden") {
		t.Fatal("sensitive or disabled output leaked")
	}
	r := records(t, sink.Bytes())
	if len(r) != 2 || r[0]["event"] != "job.started" || r[0]["instance_id"] == "" {
		t.Fatalf("bad records: %+v", r)
	}
	if _, err := time.Parse(time.RFC3339Nano, r[0]["time"].(string)); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"json", "text"} {
		var bounded bytes.Buffer
		out, err := New(&bounded, Options{Level: "debug", Format: format, Paths: true})
		if err != nil {
			t.Fatal(err)
		}
		out.Logger.Info(strings.Repeat("\x00", 500), "event", "file.failed", "job_id", strings.Repeat("\x01", 500), "run_id", strings.Repeat("\x02", 500), "relative_path", strings.Repeat("猫", 800), "error_code", "invalid_image", "asset_id", strings.Repeat("\x03", 500))
		out.Logger.Info("absolute paths excluded", "event", "file.failed", "relative_path", "C:\\private\\secret.jpg")
		closeOutput(t, out)
		if !utf8.Valid(bounded.Bytes()) || bytes.Count(bounded.Bytes(), []byte{'\n'}) != 2 {
			t.Fatal("invalid UTF-8 or multiline record")
		}
		for _, line := range bytes.Split(bytes.TrimSpace(bounded.Bytes()), []byte{'\n'}) {
			if len(line)+1 > maxRecordBytes {
				t.Fatalf("%s record too large: %d", format, len(line))
			}
		}
		if format == "json" {
			r := records(t, bounded.Bytes())
			if r[0]["truncated"] != true {
				t.Fatal("truncation not marked")
			}
			if _, ok := r[1]["relative_path"]; ok {
				t.Fatal("absolute path leaked")
			}
		}
	}
}

type gatedWriter struct {
	entered, release chan struct{}
	once             sync.Once
	bytes.Buffer
}

func (w *gatedWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered); <-w.release })
	return w.Buffer.Write(p)
}

func TestBlockedOutputDoesNotBlockProducersOrShutdown(t *testing.T) {
	sink := &gatedWriter{entered: make(chan struct{}), release: make(chan struct{})}
	o, err := New(sink, Options{})
	if err != nil {
		t.Fatal(err)
	}
	released := false
	defer func() {
		if !released {
			close(sink.release)
		}
		closeOutput(t, o)
	}()
	o.Logger.Info("start", "event", "job.started")
	select {
	case <-sink.entered:
	case <-time.After(time.Second):
		t.Fatal("writer did not start")
	}
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			o.Logger.Info("work", "event", "job.progress")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("logging blocked the producer")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(o.Close(ctx), context.Canceled) {
		t.Fatal("blocked writer prevented bounded shutdown")
	}
	close(sink.release)
	released = true
	closeOutput(t, o)
	var business, dropped int
	for _, r := range records(t, sink.Bytes()) {
		if r["event"] == "logging.dropped" {
			dropped += int(r["dropped_count"].(float64))
		} else {
			business++
		}
	}
	if business+dropped != 1001 || dropped == 0 {
		t.Fatalf("unaccounted records: emitted=%d dropped=%d", business, dropped)
	}
}

type failingWriter struct {
	calls int
	bytes.Buffer
}

func (w *failingWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.calls == 1 {
		return 0, errors.New("sink failure")
	}
	return w.Buffer.Write(p)
}
func TestWriterFailureIsReportedAfterRecovery(t *testing.T) {
	sink := &failingWriter{}
	o, err := New(sink, Options{})
	if err != nil {
		t.Fatal(err)
	}
	o.Logger.Info("first", "event", "job.started")
	o.Logger.Info("second", "event", "job.finished")
	closeOutput(t, o)
	r := records(t, sink.Bytes())
	if len(r) != 2 || r[1]["event"] != "logging.dropped" || r[1]["info_dropped"] != float64(1) {
		t.Fatalf("missing loss summary: %+v", r)
	}
}

func TestDropSummaryRespectsErrorThreshold(t *testing.T) {
	sink := &failingWriter{}
	o, err := New(sink, Options{Level: "error"})
	if err != nil {
		t.Fatal(err)
	}
	o.Logger.Error("first", "event", "server.failed")
	o.Logger.Error("second", "event", "server.failed")
	closeOutput(t, o)
	for _, r := range records(t, sink.Bytes()) {
		if r["level"] != "ERROR" {
			t.Fatalf("record below requested threshold: %+v", r)
		}
	}
}
func TestInvalidOutputOptions(t *testing.T) {
	for _, options := range []Options{{Level: "verbose"}, {Format: "xml"}} {
		if _, err := New(&bytes.Buffer{}, options); err == nil {
			t.Fatal("accepted invalid logging option")
		}
	}
}

func TestExplicitRelativePathAndGroup(t *testing.T) {
	var sink bytes.Buffer
	o, err := New(&sink, Options{Paths: true})
	if err != nil {
		t.Fatal(err)
	}
	o.Logger.With("job_id", "job").WithGroup("diagnostic").With("relative_path", "archive.zip!/inside/猫.jpg").Info("file", "event", "file.processed")
	closeOutput(t, o)
	r := records(t, sink.Bytes())[0]
	if r["job_id"] != "job" || r["diagnostic"].(map[string]any)["relative_path"] != "archive.zip!/inside/猫.jpg" {
		t.Fatalf("group attributes lost: %+v", r)
	}
}
