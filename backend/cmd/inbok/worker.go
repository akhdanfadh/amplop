package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"inbok/internal/config"
	"inbok/internal/dbos"
)

// worker runs background work until ctx is cancelled. Once it runs, it
// answers health checks on INBOK_HTTP_ADDR, so the image's inbok health
// checks it as it checks serve. It returns the first error, joined with
// any error stopping DBOS.
func worker(ctx context.Context, logger *slog.Logger) (err error) {
	url, err := config.DatabaseURL()
	if err != nil {
		return err
	}
	addr, err := config.WorkerHTTPAddr()
	if err != nil {
		return err
	}
	id, err := config.WorkerID()
	if err != nil {
		return err
	}

	w, err := dbos.NewWorker(url, id, logger)
	if err != nil {
		return fmt.Errorf("open DBOS: %w", err)
	}
	// Launch shuts DBOS down itself when it fails.
	if err := w.Launch(); err != nil {
		return fmt.Errorf("launch DBOS: %w", err)
	}
	defer func() { err = errors.Join(err, w.Shutdown()) }()

	// The worker starts no password reset work, so it has none to wait for.
	return listen(ctx, addr, workerHealth(), nil, logger)
}
