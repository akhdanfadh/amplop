package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"inbok/internal/config"
	"inbok/internal/dbos"
	"inbok/internal/emailverification"
	"inbok/internal/registration"
	"inbok/internal/repository/postgres"
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

	// DBOS names each scheduled run after its time in the process's local
	// zone, offset included, so workers in different zones would name one
	// hour's run twice and run it twice. Every worker counts in UTC; its
	// log times are UTC too. The image is UTC already.
	time.Local = time.UTC

	db, err := postgres.Open(ctx, url)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}

	w, err := dbos.NewWorker(url, id, dbos.Services{
		EmailVerificationCleanup: emailverification.NewCleanup(db, time.Now),
		RegistrationCleanup:      registration.NewCleanup(db, time.Now),
	}, logger)
	if err != nil {
		db.Close()
		return fmt.Errorf("open DBOS: %w", err)
	}
	// Launch shuts DBOS down itself when it fails.
	if err := w.Launch(); err != nil {
		db.Close()
		return fmt.Errorf("launch DBOS: %w", err)
	}
	defer func() {
		shutdownErr := w.Shutdown()
		// A step still running past the shutdown budget may hold a
		// connection, and Close would wait for it; exiting frees it.
		if shutdownErr == nil {
			db.Close()
		}
		err = errors.Join(err, shutdownErr)
	}()

	// The worker starts no password reset work, so it has none to wait for.
	return listen(ctx, addr, workerHealth(), nil, logger)
}
