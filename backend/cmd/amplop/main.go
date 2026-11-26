package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"amplop/internal/account"
	"amplop/internal/config"
	"amplop/internal/connectedaccount"
	"amplop/internal/conversation"
	"amplop/internal/dbos"
	"amplop/internal/emailverification"
	"amplop/internal/google"
	"amplop/internal/httpapi"
	"amplop/internal/label"
	"amplop/internal/logging"
	"amplop/internal/mailsync"
	"amplop/internal/meilisearch"
	"amplop/internal/messagecontent"
	"amplop/internal/outgoingmessage"
	"amplop/internal/passwordreset"
	"amplop/internal/registration"
	"amplop/internal/repository/postgres"
	"amplop/internal/s3"
	"amplop/internal/search"
	"amplop/internal/session"
	"amplop/internal/smtp"
	"amplop/internal/systememail"
	"amplop/internal/totp"
	"amplop/internal/webapp"
)

const (
	exportPerm = 0o644 // new files: owner writes, everyone reads

	// Server limits.

	// ReadHeaderTimeout stops clients that send headers slowly to hold
	// connections open (Slowloris).
	readHeaderTimeout = 5 * time.Second
	// readTimeout and [writeTimeout] cap a whole request body and response:
	// a slow upload or a streaming response (e.g. server-sent events)
	// must extend its own deadline with [http.ResponseController].
	readTimeout  = 30 * time.Second
	writeTimeout = 30 * time.Second
	idleTimeout  = 120 * time.Second

	// shutdownTimeout bounds how long in-flight requests may finish.
	// It stays below Docker's default 10s grace period before SIGKILL.
	shutdownTimeout = 5 * time.Second
)

var errUsage = errors.New("usage: amplop serve | amplop worker | amplop migrate | amplop health | amplop export <path> | amplop recover-admin <email> | amplop search rebuild")

func main() {
	// One root logger for every service, so their lines carry request_id.
	logger := logging.New(os.Stderr)

	// SIGINT (Ctrl+C) and SIGTERM (docker stop) cancel ctx. The first
	// signal also restores default handling, so a second one exits at
	// once instead of waiting for shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	context.AfterFunc(ctx, stop)
	err := run(ctx, os.Args[1:], logger)
	stop()

	if err != nil {
		logger.Error("amplop failed", "err", err)
		os.Exit(1)
	}
}

// run executes one command. Example: amplop export openapi.json
func run(ctx context.Context, args []string, logger *slog.Logger) error {
	if len(args) == 0 {
		return errUsage
	}

	switch {
	case args[0] == "serve" && len(args) == 1:
		return serve(ctx, logger)
	case args[0] == "worker" && len(args) == 1:
		return worker(ctx, logger)
	case args[0] == "migrate" && len(args) == 1:
		return migrate(ctx, logger)
	case args[0] == "health" && len(args) == 1:
		return health(ctx, healthTimeout)
	case args[0] == "recover-admin" && len(args) == 2:
		return recoverAdmin(ctx, args[1])
	case args[0] == "export" && len(args) == 2:
		return export(args[1], logger)
	case args[0] == "search" && len(args) == 2 && args[1] == "rebuild":
		return searchRebuild(ctx, os.Stdout)
	}
	return errUsage
}

// serve connects to the database and opens a DBOS client for the jobs
// requests save, then serves the API until ctx is cancelled.
func serve(ctx context.Context, logger *slog.Logger) (err error) {
	// 1. Read the settings, failing before anything starts.
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	url, err := config.DatabaseURL()
	if err != nil {
		return err
	}
	searchURL, searchKey, err := config.SearchIndex()
	if err != nil {
		return err
	}

	// 2. Open the database.
	db, err := postgres.Open(ctx, url)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	// 3. Open the DBOS client that saves background jobs with the changes
	// that need them, e.g. a reset link's with its request; the worker runs
	// them. Not ctx: it ends at the shutdown signal, while requests still
	// drain; Close stops the client after them.
	jobs, err := dbos.NewClient(context.WithoutCancel(ctx), url, logger)
	if err != nil {
		return fmt.Errorf("open DBOS client: %w", err)
	}
	defer func() { err = errors.Join(err, jobs.Close()) }()
	db = db.WithWorkflows(jobs)

	// 4. Warn when setup is done but its token is still configured.
	accounts := account.NewService(db, cfg.SetupToken)
	if cfg.SetupToken != "" {
		status, err := accounts.SetupStatus(ctx)
		if err != nil {
			return fmt.Errorf("check setup status: %w", err)
		}
		if status == account.SetupDone {
			logger.Warn("setup is complete; remove AMPLOP_SETUP_TOKEN from configuration")
		}
	}

	// 5. Build the services and the API.
	throttle := session.NewThrottle(db, time.Now)
	codeThrottle := totp.NewThrottle(db, time.Now, logger)
	sessions := session.NewService(db, accounts, throttle, time.Now)
	mailer := systememail.NewMailer(db, cfg.Encryption, smtp.NewSender(cfg.PublicURL.Hostname()), logger)
	connectedAccounts := connectedaccount.NewService(db, cfg.Encryption)
	services := httpapi.Services{
		EmailVerification:       emailverification.NewService(db, mailer, cfg.PublicURL, time.Now),
		Members:                 account.NewMembers(db, time.Now),
		SystemEmail:             systememail.NewService(db, cfg.Encryption),
		TestMessages:            systememail.NewTestMessages(db, mailer, time.Now),
		Registration:            registration.NewService(db),
		RegistrationRequests:    registration.NewRequests(db, mailer, cfg.PublicURL, time.Now, logger),
		RegistrationCompletion:  registration.NewCompletion(db, time.Now),
		PasswordResetRequests:   passwordreset.NewRequests(db, time.Now),
		PasswordResetCompletion: passwordreset.NewCompletion(db, time.Now),
		Disabler:                totp.NewDisabler(db, cfg.Encryption, totp.NewVerifier(time.Now), codeThrottle),
		Backups:                 totp.NewBackups(db, cfg.Encryption, totp.NewVerifier(time.Now), codeThrottle, time.Now),
		CodeSignIn:              session.NewCodeSignIn(db, cfg.Encryption, totp.NewVerifier(time.Now), codeThrottle, time.Now),
		Enrollment:              session.NewEnrollment(db, time.Now),
		TOTP:                    totp.NewService(db, cfg.Encryption, sessions, totp.NewVerifier(time.Now), codeThrottle, time.Now),
		Account:                 accounts,
		Session:                 sessions,
		FirstAdminSignIn:        sessions,
		Throttle:                throttle,
		ConnectedAccounts:       connectedAccounts,
		OutgoingMessages:        outgoingmessage.NewService(db, connectedAccounts, time.Now),
		Syncs:                   mailsync.NewService(db, jobs, time.Now),
		Conversations:           conversation.NewService(db, google.WebApp{}),
		// Meilisearch is asked only when a member searches, so an index
		// that is down keeps no one from signing in.
		Search: search.NewSearcher(meilisearch.New(searchURL, searchKey), db),
	}
	// Without the bucket check of the worker's s3.Open: a store that is
	// down then stops no one signing in, only reading message content.
	content := s3.New(s3Settings(cfg.S3))
	services.MessageContent = messagecontent.NewReader(content, db, nil)
	if cfg.Google != nil {
		// Only when configured, so the interface stays nil and Gmail is not
		// offered otherwise.
		client := googleClient(cfg.Google, cfg.PublicURL)
		services.Connections = connectedaccount.NewConnections(db, connectedAccounts, client, time.Now)
		// Content a member opens before the worker stored it is fetched in
		// the request, at this process's own Gmail pace.
		access := connectedaccount.NewAccess(db, cfg.Encryption, client, time.Now)
		fetcher := messagecontent.NewFetcher(messagecontent.NewContents(content, db), db, client, access, time.Now, logger)
		services.MessageContent = messagecontent.NewReader(content, db, fetcher)
		services.ConversationActions = conversation.NewActionService(db, access, client, logger)
		services.Labels = label.NewService(db, access, client, logger)
	}
	api := httpapi.New(logger, services)
	if err := api.SetTrustedProxies(cfg.TrustedProxies); err != nil {
		return fmt.Errorf("configure trusted proxies: %w", err)
	}

	// 6. Serve the web app when this build has one.
	build, err := webapp.Load(webapp.Files())
	switch {
	case errors.Is(err, webapp.ErrNoBuild):
		logger.Warn("this build has no web app; paths outside /api answer 404")
	case err != nil:
		return fmt.Errorf("load web app: %w", err)
	default:
		api.ServeWebApp(build)
	}

	// 7. Serve until ctx is cancelled.
	return listen(ctx, cfg.HTTPAddr, api.Handler(), logger)
}

// googleClient returns the Google client for settings, sending members back
// to the amplop served at publicURL.
func googleClient(settings *config.Google, publicURL *url.URL) *google.Client {
	return google.New(google.Settings{
		ClientID:     settings.ClientID,
		ClientSecret: settings.ClientSecret,
		RedirectURL:  httpapi.GoogleCallbackURL(publicURL),
		BaseURL:      settings.BaseURL,
	}, time.Now)
}

// listen serves handler on addr until ctx is cancelled, then lets in-flight
// requests finish. It returns the error that stopped serving, or the
// shutdown's error, such as the context's when requests outlast the timeout.
func listen(ctx context.Context, addr string, handler http.Handler, logger *slog.Logger) error {
	// ErrorLog routes net/http's internal errors, e.g. a failed TLS
	// handshake, into the same log.
	server := http.Server{
		Handler:           handler,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	// 1. Bind before logging, so a busy port never logs a false start.
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	// 2. Serve until ctx is cancelled or serving fails.
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	logger.Info("starting HTTP server", "addr", listener.Addr().String())

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}

	// 3. Stop taking requests and let in-flight ones finish.
	logger.Info("shutting down HTTP server")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	return server.Shutdown(shutdownCtx)
}

// migrate applies pending database migrations, then exits. serve never
// migrates: a deploy runs migrate once before starting servers.
func migrate(ctx context.Context, logger *slog.Logger) error {
	url, err := config.DatabaseURL()
	if err != nil {
		return err
	}
	return postgres.Migrate(ctx, url, logger)
}

// export writes the OpenAPI document to path without starting a listener.
func export(path string, logger *slog.Logger) error {
	doc, err := httpapi.New(logger, httpapi.Services{}).OpenAPI()
	if err != nil {
		return err
	}
	return os.WriteFile(path, doc, exportPerm)
}
