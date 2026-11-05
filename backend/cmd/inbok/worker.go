package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"inbok/internal/account"
	"inbok/internal/config"
	"inbok/internal/dbos"
	"inbok/internal/emailverification"
	"inbok/internal/passwordreset"
	"inbok/internal/registration"
	"inbok/internal/repository/postgres"
	"inbok/internal/smtp"
	"inbok/internal/systememail"
)

// backgroundSendTimeout bounds one email the worker sends. No request
// waits for it, so a slow server gets time to answer the message, which
// makes a send cut short there, and so possibly sent twice, rarer: RFC 5321
// section 4.5.3.2.6 gives a server up to 10 minutes for that answer.
const backgroundSendTimeout = 5 * time.Minute

// worker runs background work until ctx is cancelled. Once it runs, it
// answers health checks on INBOK_HTTP_ADDR, so the image's inbok health
// checks it as it checks serve. It returns the first error, joined with
// any error stopping DBOS.
func worker(ctx context.Context, logger *slog.Logger) (err error) {
	// 1. Read the settings, failing before anything starts.
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
	// The mailer decrypts the saved SMTP password, and the public URL starts
	// the notice's links and names inbok to mail servers.
	publicURL, err := config.PublicURL()
	if err != nil {
		return err
	}
	keys, err := config.EncryptionKeys()
	if err != nil {
		return err
	}

	// 2. Count time in UTC. DBOS names each scheduled run after its time in
	// the process's local zone, offset included, so workers in different zones would name one
	// hour's run twice and run it twice. Every worker counts in UTC; its
	// log times are UTC too. The image is UTC already.
	time.Local = time.UTC

	// 3. Open the database and build the services the workflows call.
	db, err := postgres.Open(ctx, url)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}

	sender := smtp.NewSender(publicURL.Hostname()).WithTimeout(backgroundSendTimeout)
	mailer := systememail.NewMailer(db, keys, sender, logger)
	w, err := dbos.NewWorker(url, id, dbos.Services{
		EmailVerificationCleanup:    emailverification.NewCleanup(db, time.Now),
		RegistrationCleanup:         registration.NewCleanup(db, time.Now),
		PasswordResetCleanup:        passwordreset.NewCleanup(db, time.Now),
		PasswordResetAttemptCleanup: passwordreset.NewAttemptCleanup(db, time.Now),
		RecoveryNotices:             account.NewRecoveryNotices(db, mailer, publicURL),
	}, logger)
	if err != nil {
		db.Close()
		return fmt.Errorf("open DBOS: %w", err)
	}

	// 4. Launch DBOS: it resumes this worker's unfinished runs, then takes
	// queued and scheduled ones. Launch shuts DBOS down itself when it
	// fails.
	if err := w.Launch(); err != nil {
		db.Close()
		return fmt.Errorf("launch DBOS: %w", err)
	}

	// 5. Once step 6 returns, stop DBOS, which leaves unfinished runs
	// pending for the next start.
	defer func() {
		shutdownErr := w.Shutdown()
		// A step still running past the shutdown budget may hold a
		// connection, and Close would wait for it; exiting frees it.
		if shutdownErr == nil {
			db.Close()
		}
		err = errors.Join(err, shutdownErr)
	}()

	// 6. Answer health checks until ctx is cancelled. The worker starts no
	// password reset work, so it has none to wait for.
	return listen(ctx, addr, workerHealth(), nil, logger)
}
