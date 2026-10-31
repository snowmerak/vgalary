package main

import (
	"context"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/snowmerak/vgalary/internal/backend"
)

func main() {
	configDir, err := os.UserConfigDir()
	if err != nil {
		slog.Error("locate app data", "error", err)
		os.Exit(1)
	}
	data := flag.String("data-dir", filepath.Join(configDir, "vgalary"), "persistent app data directory")
	listen := flag.String("listen", "127.0.0.1:8181", "loopback HTTP address")
	flag.Parse()
	address, err := net.ResolveTCPAddr("tcp", *listen)
	if err != nil || address.IP == nil || !address.IP.IsLoopback() {
		slog.Error("listen must be a loopback address")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	app, err := backend.New(ctx, *data)
	if err != nil {
		slog.Error("open backend", "error", err)
		os.Exit(1)
	}
	server := &http.Server{Addr: *listen, Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	serveErr := make(chan error, 1)
	go func() {
		slog.Info("backend listening", "address", *listen, "data_dir", *data)
		serveErr <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
	case err := <-serveErr:
		if err != http.ErrServerClosed {
			slog.Error("HTTP server failed", "error", err)
		}
	}
	stop()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
		slog.Error("HTTP shutdown", "error", err)
	}
	if err := app.Close(); err != nil {
		slog.Error("close backend", "error", err)
	}
}
