package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"inbok/internal/config"
	"inbok/internal/httpapi"
)

const (
	exportPerm = 0o644 // new files: owner writes, everyone reads

	// Server limits.

	// ReadHeaderTimeout stops clients that send headers slowly to hold
	// connections open (Slowloris).
	readHeaderTimeout = 5 * time.Second
	// readTimeout and writeTimeout cap a whole request body and response:
	// a slow upload or a streaming response (e.g. server-sent events)
	// must extend its own deadline with [http.ResponseController].
	readTimeout  = 30 * time.Second
	writeTimeout = 30 * time.Second
	idleTimeout  = 120 * time.Second

	// shutdownTimeout bounds how long in-flight requests may finish.
	// It stays below Docker's default 10s grace period before SIGKILL.
	shutdownTimeout = 5 * time.Second
)

var errUsage = errors.New("usage: inbok serve | inbok export <path>")

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	// SIGINT (Ctrl+C) and SIGTERM (docker stop) cancel ctx. The first
	// signal also restores default handling, so a second one exits at
	// once instead of waiting for shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	context.AfterFunc(ctx, stop)
	err := run(ctx, os.Args[1:], logger)
	stop()

	if err != nil {
		logger.Error("inbok failed", "err", err)
		os.Exit(1)
	}
}

// run executes one command. Example: inbok export openapi.json
func run(ctx context.Context, args []string, logger *slog.Logger) error {
	if len(args) == 0 {
		return errUsage
	}

	switch {
	case args[0] == "serve" && len(args) == 1:
		return serve(ctx, logger)
	case args[0] == "export" && len(args) == 2:
		return export(args[1], logger)
	}
	return errUsage
}

// serve listens until ctx is cancelled, then lets in-flight requests finish.
func serve(ctx context.Context, logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// ErrorLog routes net/http's internal errors, e.g. a failed TLS
	// handshake, into the same log.
	server := http.Server{
		Handler:           httpapi.New(logger).Handler(),
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	// Bind before logging, so a busy port never logs a false start.
	listener, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return err
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	logger.Info("starting HTTP server", "addr", listener.Addr().String())

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}

	logger.Info("shutting down HTTP server")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	return server.Shutdown(shutdownCtx)
}

// export writes the OpenAPI document to path without starting a listener.
func export(path string, logger *slog.Logger) error {
	doc, err := httpapi.New(logger).OpenAPI()
	if err != nil {
		return err
	}
	return os.WriteFile(path, doc, exportPerm)
}
