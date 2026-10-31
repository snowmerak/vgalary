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
	"syscall"
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
	publicOrigin := flag.String("public-origin", "", "HTTPS origin accepted behind your reverse proxy")
	passwordEnv := flag.String("web-password-env", "", "environment variable containing the web password (defaults to VGALARY_WEB_PASSWORD in domain mode)")
	flag.Parse()
	address, err := net.ResolveTCPAddr("tcp", *listen)
	if err != nil || address.IP == nil || !address.IP.IsLoopback() {
		slog.Error("listen must be a loopback address")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	app, err := backend.New(ctx, *data)
	if err != nil {
		slog.Error("open backend", "error", err)
		os.Exit(1)
	}
	handler, err := app.HandlerWithOptions(backend.HTTPOptions{PublicOrigin: *publicOrigin, PasswordEnv: *passwordEnv})
	if err != nil {
		_ = app.Close()
		slog.Error("web access configuration", "error", err)
		os.Exit(1)
	}
	server := &http.Server{Addr: *listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	failed := false
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
			failed = true
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
	if failed {
		os.Exit(1)
	}
}
