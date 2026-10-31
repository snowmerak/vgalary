package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFailedBindNeverReportsStarted(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	var output bytes.Buffer
	code := run([]string{"-data-dir", t.TempDir(), "-listen", listener.Addr().String()}, &output)
	if code != 1 || !strings.Contains(output.String(), `"error_code":"listen_failed"`) || strings.Contains(output.String(), "server.started") {
		t.Fatalf("bind failure claimed readiness: %s", output.String())
	}
}
func TestInvalidLoggingFlagsAndHelp(t *testing.T) {
	for _, arguments := range [][]string{{"-log-level", "verbose"}, {"-log-format", "xml"}, {"-unknown", "SECRET-argument"}} {
		var output bytes.Buffer
		if run(arguments, &output) != 1 || strings.Contains(output.String(), "SECRET-argument") {
			t.Fatalf("bad invalid-option handling: %s", output.String())
		}
	}
	var help bytes.Buffer
	if run([]string{"-h"}, &help) != 0 || !strings.Contains(help.String(), "-log-paths") {
		t.Fatal("logging help missing")
	}
}

type runtimeLogs struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (w *runtimeLogs) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.data.Write(p)
}
func (w *runtimeLogs) lines() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return strings.Split(strings.TrimSpace(w.data.String()), "\n")
}
func TestIsolatedServerLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logs := &runtimeLogs{}
	done := make(chan int, 1)
	go func() { done <- runContext(ctx, []string{"-listen", "127.0.0.1:0", "-data-dir", t.TempDir()}, logs) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("test server did not stop")
		}
	}()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	address := ""
	for address == "" {
		for _, line := range logs.lines() {
			var r map[string]any
			if json.Unmarshal([]byte(line), &r) == nil && r["event"] == "server.started" {
				address, _ = r["listen_address"].(string)
			}
		}
		if address != "" {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("test server did not start")
		case <-tick.C:
		}
	}
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get("http://" + address + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	cancel()
	select {
	case code := <-done:
		done <- code
		if code != 0 {
			t.Fatal(code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("test server did not stop")
	}
	joined := strings.Join(logs.lines(), "\n")
	for _, event := range []string{"server.started", "server.stopping", "server.stopped"} {
		if !strings.Contains(joined, `"event":"`+event+`"`) {
			t.Fatalf("missing lifecycle event %s: %s", event, joined)
		}
	}
}
