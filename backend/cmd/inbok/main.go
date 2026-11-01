package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"inbok/internal/account"
	"inbok/internal/config"
	"inbok/internal/emailverification"
	"inbok/internal/httpapi"
	"inbok/internal/logging"
	"inbok/internal/probe"
	"inbok/internal/registration"
	"inbok/internal/repository/postgres"
	"inbok/internal/session"
	"inbok/internal/smtp"
	"inbok/internal/systememail"
	"inbok/internal/totp"
	"inbok/internal/webapp"
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

var errUsage = errors.New("usage: inbok serve | inbok migrate | inbok health | inbok export <path> | inbok recover-admin <email>")

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
	case args[0] == "migrate" && len(args) == 1:
		return migrate(ctx, logger)
	case args[0] == "health" && len(args) == 1:
		return health(ctx, healthTimeout)
	case args[0] == "recover-admin" && len(args) == 2:
		return recoverAdmin(ctx, args[1])
	case args[0] == "export" && len(args) == 2:
		return export(args[1], logger)
	}
	return errUsage
}

// serve connects to the database, then serves the API until ctx is cancelled.
func serve(ctx context.Context, logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	url, err := config.DatabaseURL()
	if err != nil {
		return err
	}

	db, err := postgres.Open(ctx, url)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	accounts := account.NewService(db, cfg.SetupToken)
	if cfg.SetupToken != "" {
		status, err := accounts.SetupStatus(ctx)
		if err != nil {
			return fmt.Errorf("check setup status: %w", err)
		}
		if status == account.SetupDone {
			logger.Warn("setup is complete; remove INBOK_SETUP_TOKEN from configuration")
		}
	}

	throttle := session.NewThrottle(db, time.Now)
	codeThrottle := totp.NewThrottle(db, time.Now, logger)
	mailer := systememail.NewMailer(db, cfg.Encryption, smtp.NewSender(cfg.PublicURL.Hostname()), logger)
	services := httpapi.Services{
		EmailVerification: emailverification.NewService(db, mailer, cfg.PublicURL, time.Now),
		Members:           account.NewMembers(db, time.Now),
		SystemEmail:       systememail.NewService(db, cfg.Encryption),
		TestMessages:      systememail.NewTestMessages(db, mailer, time.Now),
		Registration:      registration.NewService(db),
		Disabler:          totp.NewDisabler(db, cfg.Encryption, totp.NewVerifier(time.Now), codeThrottle),
		Backups:           totp.NewBackups(db, cfg.Encryption, totp.NewVerifier(time.Now), codeThrottle),
		CodeSignIn:        session.NewCodeSignIn(db, cfg.Encryption, totp.NewVerifier(time.Now), codeThrottle, time.Now),
		Enrollment:        session.NewEnrollment(db, cfg.Encryption, totp.NewVerifier(time.Now), codeThrottle, time.Now),
		TOTP:              totp.NewService(db, cfg.Encryption),
		Probe:             probe.NewService(db),
		Account:           accounts,
		Session:           session.NewService(db, accounts, throttle, time.Now),
		Throttle:          throttle,
	}
	api := httpapi.New(logger, services)
	if err := api.SetTrustedProxies(cfg.TrustedProxies); err != nil {
		return fmt.Errorf("configure trusted proxies: %w", err)
	}

	build, err := webapp.Load(webapp.Files())
	switch {
	case errors.Is(err, webapp.ErrNoBuild):
		logger.Warn("this build has no web app; paths outside /api answer 404")
	case err != nil:
		return fmt.Errorf("load web app: %w", err)
	default:
		api.ServeWebApp(build)
	}

	return listen(ctx, cfg.HTTPAddr, api.Handler(), logger)
}

// listen serves handler on addr until ctx is cancelled, then lets in-flight
// requests finish.
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

	// Bind before logging, so a busy port never logs a false start.
	listener, err := net.Listen("tcp", addr)
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
