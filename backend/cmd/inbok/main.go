package main

import (
	"errors"
	"log/slog"
	"net/http"
	"os"

	"inbok/internal/config"
	"inbok/internal/httpapi"
)

const exportPerm = 0o644 // new files: owner writes, everyone reads

var errUsage = errors.New("usage: inbok serve | inbok export <path>")

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	if err := run(os.Args[1:], logger); err != nil {
		logger.Error("inbok failed", "err", err)
		os.Exit(1)
	}
}

// run executes one command. Example: inbok export openapi.json
func run(args []string, logger *slog.Logger) error {
	if len(args) == 0 {
		return errUsage
	}

	switch {
	case args[0] == "serve" && len(args) == 1:
		return serve(logger)
	case args[0] == "export" && len(args) == 2:
		return export(args[1], logger)
	}
	return errUsage
}

func serve(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// ErrorLog routes net/http's internal errors, e.g. a panic in a
	// handler, into the same log.
	server := http.Server{
		Addr:     cfg.HTTPAddr,
		Handler:  httpapi.New(logger).Handler(),
		ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	logger.Info("starting HTTP server", "addr", cfg.HTTPAddr)
	return server.ListenAndServe()
}

// export writes the OpenAPI document to path without starting a listener.
func export(path string, logger *slog.Logger) error {
	doc, err := httpapi.New(logger).OpenAPI()
	if err != nil {
		return err
	}
	return os.WriteFile(path, doc, exportPerm)
}
