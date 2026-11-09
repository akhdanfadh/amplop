package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"inbok/internal/account"
	"inbok/internal/config"
	"inbok/internal/connectedaccount"
	"inbok/internal/dbos"
	"inbok/internal/emailverification"
	"inbok/internal/google"
	"inbok/internal/meilisearch"
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

// prepareSearchTimeout bounds applying the search index's settings at
// startup, so a Meilisearch that never answers stops the worker with an
// error instead of holding it silent. A change to the settings reindexes
// every message, so it allows far more than a request's 10 seconds. The
// task goes on in Meilisearch, and the worker's next start waits for it
// again.
const prepareSearchTimeout = time.Minute

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
	// The Google client revokes disconnected accounts' tokens.
	googleSettings, err := config.GoogleSettings()
	if err != nil {
		return err
	}
	searchURL, searchKey, err := config.SearchIndex()
	if err != nil {
		return err
	}

	// 2. Count time in UTC. DBOS names each scheduled run after its time in
	// the process's local zone, offset included, so workers in different zones would name one
	// hour's run twice and run it twice. Every worker counts in UTC; its
	// log times are UTC too. The image is UTC already.
	time.Local = time.UTC

	// 3. Apply the search index's settings before any job writes to it. The
	// worker does so, not serve: it is the index's writer, and a search
	// index that is down then stops no one signing in.
	index := meilisearch.New(searchURL, searchKey)
	prepareCtx, cancel := context.WithTimeout(ctx, prepareSearchTimeout)
	err = index.Prepare(prepareCtx)
	cancel()
	if err != nil {
		return err
	}

	// 4. Open the database and build the services the workflows call.
	db, err := postgres.Open(ctx, url)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}

	sender := smtp.NewSender(publicURL.Hostname()).WithTimeout(backgroundSendTimeout)
	mailer := systememail.NewMailer(db, keys, sender, logger)
	services := dbos.Services{
		EmailVerificationCleanup:    emailverification.NewCleanup(db, time.Now),
		RegistrationCleanup:         registration.NewCleanup(db, time.Now),
		PasswordResetCleanup:        passwordreset.NewCleanup(db, time.Now),
		PasswordResetAttemptCleanup: passwordreset.NewAttemptCleanup(db, time.Now),
		ConnectStateCleanup:         connectedaccount.NewStateCleanup(db, time.Now),
		RecoveryNotices:             account.NewRecoveryNotices(db, mailer, publicURL),
		PasswordResetLinks:          passwordreset.NewLinks(db, mailer, publicURL, time.Now),
		PasswordChangedNotices:      passwordreset.NewNotices(db, mailer, publicURL),
	}
	if googleSettings != nil {
		// Only when configured, so the interface stays nil: removals saved
		// while a client was configured then wait for one. Revoking and
		// refreshing send no redirect URI, so the worker's client has none.
		client := google.New(google.Settings{
			ClientID:     googleSettings.ClientID,
			ClientSecret: googleSettings.ClientSecret,
			BaseURL:      googleSettings.BaseURL,
		}, time.Now)
		services.AccountRemovals = connectedaccount.NewRemovals(db, keys, client)
	}
	w, err := dbos.NewWorker(url, id, services, logger)
	if err != nil {
		db.Close()
		return fmt.Errorf("open DBOS: %w", err)
	}

	// 5. Launch DBOS: it resumes this worker's unfinished runs, then takes
	// queued and scheduled ones. Launch shuts DBOS down itself when it
	// fails.
	if err := w.Launch(); err != nil {
		db.Close()
		return fmt.Errorf("launch DBOS: %w", err)
	}

	// 6. Once step 7 returns, stop DBOS, which leaves unfinished runs
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

	// 7. Answer health checks until ctx is cancelled.
	return listen(ctx, addr, workerHealth(), logger)
}
