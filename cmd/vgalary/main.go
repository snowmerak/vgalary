package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/snowmerak/vgalary/internal/backend"
	"github.com/snowmerak/vgalary/internal/diagnostic"
)

func main() { os.Exit(run(os.Args[1:], os.Stderr)) }

func earlyFailure(output io.Writer, code string) int {
	logs, err := diagnostic.New(output, diagnostic.Options{})
	if err != nil {
		return 1
	}
	logs.Logger.Error("server failed", "event", "server.failed", "error_code", code)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = logs.Close(ctx)
	return 1
}

func run(arguments []string, output io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runContext(ctx, arguments, output)
}

func runContext(parent context.Context, arguments []string, output io.Writer) int {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return earlyFailure(output, "config_directory_unavailable")
	}
	flags := flag.NewFlagSet("vgalary", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	data := flags.String("data-dir", filepath.Join(configDir, "vgalary"), "persistent app data directory")
	listen := flags.String("listen", "127.0.0.1:8181", "loopback HTTP address")
	publicOrigin := flags.String("public-origin", "", "HTTPS origin accepted behind your reverse proxy")
	passwordEnv := flags.String("web-password-env", "", "web password environment variable name")
	logLevel := flags.String("log-level", "info", "minimum diagnostic level: debug, info, warn, error")
	logFormat := flags.String("log-format", "json", "diagnostic format: json or text")
	logPaths := flags.Bool("log-paths", false, "include source-relative paths in diagnostic output")
	if err := flags.Parse(arguments); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			flags.SetOutput(output)
			flags.PrintDefaults()
			return 0
		}
		return earlyFailure(output, "invalid_arguments")
	}
	logs, err := diagnostic.New(output, diagnostic.Options{Level: *logLevel, Format: *logFormat, Paths: *logPaths})
	if err != nil {
		return earlyFailure(output, "invalid_logging_options")
	}
	var shutdownDeadline time.Time
	defer func() {
		deadline := time.Now().Add(time.Second)
		if !shutdownDeadline.IsZero() && shutdownDeadline.Before(deadline) {
			deadline = shutdownDeadline
		}
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		defer cancel()
		_ = logs.Close(ctx)
	}()
	logger := logs.Logger
	failed := func(code string) { logger.Error("server failed", "event", "server.failed", "error_code", code) }
	address, err := net.ResolveTCPAddr("tcp", *listen)
	if err != nil || address.IP == nil || !address.IP.IsLoopback() {
		failed("invalid_listen_address")
		return 1
	}
	ctx, stop := context.WithCancel(parent)
	defer stop()
	app, err := backend.New(ctx, *data, backend.WithLogger(logger))
	if err != nil {
		failed("backend_open_failed")
		return 1
	}
	handler, err := app.HandlerWithOptions(backend.HTTPOptions{PublicOrigin: *publicOrigin, PasswordEnv: *passwordEnv})
	if err != nil {
		_ = app.Close()
		failed("web_access_configuration_failed")
		return 1
	}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		_ = app.Close()
		failed("listen_failed")
		return 1
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	serveErr := make(chan error, 1)
	// Binding succeeded; a failed bind must never produce a started event.
	logger.Info("server started", "event", "server.started", "listen_address", listener.Addr().String())
	go func() { serveErr <- server.Serve(listener) }()
	code := 0
	select {
	case <-ctx.Done():
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			failed("http_server_failed")
			code = 1
		}
	}
	logger.Info("server stopping", "event", "server.stopping")
	stop()
	shutdownDeadline = time.Now().Add(10 * time.Second)
	shutdown, cancel := context.WithDeadline(context.Background(), shutdownDeadline)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
		failed("http_shutdown_failed")
		code = 1
	}
	if err := app.Close(); err != nil {
		failed("backend_close_failed")
		code = 1
	}
	level := slog.LevelInfo
	if code != 0 {
		level = slog.LevelError
	}
	logger.Log(context.Background(), level, "server stopped", "event", "server.stopped", "result", map[bool]string{true: "succeeded", false: "failed"}[code == 0])
	return code
}
