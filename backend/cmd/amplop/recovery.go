package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"

	"amplop/internal/account"
	"amplop/internal/config"
	"amplop/internal/dbos"
	"amplop/internal/emailaddress"
	"amplop/internal/repository/postgres"
)

type adminRecovery interface {
	Administrator(context.Context, string) (account.User, error)
	Recover(context.Context, string, string) (account.RecoveryResult, error)
	NoticeBlocker(context.Context) (account.NoticeOutcome, error)
}

type recoveryConsole struct {
	input        io.Reader
	output       io.Writer
	readPassword func() ([]byte, error)
}

// runRecovery keeps input and output at the command boundary, outside account rules.
func runRecovery(ctx context.Context, email string, service adminRecovery, console recoveryConsole) error {
	// 1. Find the administrator before asking for anything.
	user, err := service.Administrator(ctx, email)
	if err != nil {
		return recoveryError(err)
	}

	// 2. Read the new password, and on a terminal ask to continue.
	password, err := console.password()
	if err != nil {
		return err
	}
	if console.readPassword != nil {
		_, err := fmt.Fprintf(console.output, "Recover administrator %q. This replaces the password, removes TOTP and backup codes, ends all sessions and sign-in challenges, and clears failed attempts.\nContinue? [y/N] ", user.Email)
		if err != nil {
			return errors.New("could not write recovery confirmation")
		}
		answer, err := recoveryLine(console.input)
		if err != nil {
			return err
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "y", "yes":
		default:
			return errors.New("recovery cancelled; nothing changed")
		}
	}
	if err := ctx.Err(); err != nil {
		return errors.New("recovery interrupted before submission")
	}

	// 3. Recover, saving the notice in the same transaction.
	result, err := service.Recover(ctx, email, password)
	if err != nil {
		return recoveryError(err)
	}

	// 4. Report what changed, then whether the administrators will be emailed.
	if _, err = fmt.Fprintf(console.output,
		"Password replaced. %d TOTP factors and %d backup codes removed. %d sessions ended.\n"+
			"%d sign-in challenges, %d TOTP attempts, %d sign-in attempts, and %d password reset links cleared.\n"+
			"Sign in, then set up TOTP, which amplop asks for at once, to restore administrator access.\n",
		result.Factors, result.BackupCodes, result.Sessions,
		result.Challenges, result.TOTPAttempts, result.SignInAttempts, result.ResetLinks); err != nil {
		return errors.New("recovery completed, but could not write the summary")
	}
	if _, err := fmt.Fprintln(console.output, noticeLine(ctx, service)); err != nil {
		return errors.New("recovery completed, but could not write the summary")
	}
	return nil
}

// noticeLine returns the line telling the operator whether the worker will
// email the administrators about the recovery, and if not, why.
func noticeLine(ctx context.Context, service adminRecovery) string {
	blocker, err := service.NoticeBlocker(ctx)
	switch {
	case err != nil:
		// The recovery stands; only this check failed, and its cause can
		// hold connection details.
		return "Could not check whether the administrators will be emailed."
	case blocker == account.NoticeNotConfigured:
		return "System email is not configured; the administrators will be emailed only if it is set up and tested before the worker tries."
	case blocker == account.NoticeNotTested:
		return "System email has not passed a test message; the administrators will be emailed only if one passes before the worker tries."
	}
	return "Every active administrator will be emailed about this recovery once the worker runs."
}

// Unknown storage errors may contain credentials; only known safe errors reach logs.
func recoveryError(err error) error {
	for _, known := range []error{emailaddress.ErrInvalid, account.ErrUserNotFound, account.ErrRecoveryForbidden, account.ErrPasswordInvalid} {
		if errors.Is(err, known) {
			return known
		}
	}
	return errors.New("administrator recovery failed; success is unconfirmed")
}

// recoverAdmin composes the operator command with database-only configuration.
func recoverAdmin(ctx context.Context, email string) error {
	// 1. Open the database: the URL is the only setting.
	url, err := config.DatabaseURL()
	if err != nil {
		return err
	}
	db, err := postgres.Open(ctx, url)
	if err != nil {
		return errors.New("cannot connect to database for recovery")
	}
	defer db.Close()

	// 2. Open a DBOS client, which saves the notice for the worker in the
	// recovery's transaction. DBOS's progress lines would only clutter the
	// command's output.
	client, err := dbos.NewClient(ctx, url, slog.New(slog.DiscardHandler))
	if err != nil {
		return errors.New("cannot open background work for recovery; run amplop migrate first")
	}
	defer client.Close()

	// 3. Read from the terminal without echo, or one line from a pipe.
	console := recoveryConsole{input: os.Stdin, output: os.Stdout}
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		state, err := term.GetState(fd)
		if err != nil {
			return errors.New("cannot read terminal settings")
		}
		// Restore echo even if a signal cancels a blocked password read.
		defer term.Restore(fd, state)
		console.readPassword = func() ([]byte, error) { return term.ReadPassword(fd) }
	}

	// 4. Run the recovery with storage that can save the notice, until it ends or
	// a signal interrupts it.
	done := make(chan error, 1)
	go func() { done <- runRecovery(ctx, email, account.NewRecovery(db.WithWorkflows(client)), console) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return errors.New("recovery interrupted; success is unconfirmed")
	}
}

func (c recoveryConsole) password() (string, error) {
	if c.readPassword == nil {
		return recoveryLine(c.input)
	}
	first, err := c.secret("New password: ")
	if err != nil {
		return "", err
	}
	second, err := c.secret("Repeat password: ")
	if err != nil {
		return "", err
	}
	if first != second {
		return "", errors.New("passwords do not match; nothing changed")
	}
	return first, nil
}

func (c recoveryConsole) secret(prompt string) (string, error) {
	if _, err := fmt.Fprint(c.output, prompt); err != nil {
		return "", errors.New("could not write password prompt")
	}
	password, err := c.readPassword()
	_, writeErr := fmt.Fprintln(c.output)
	if err != nil {
		return "", errors.New("could not read password")
	}
	if writeErr != nil {
		return "", errors.New("could not write password prompt")
	}
	return string(password), nil
}

func recoveryLine(input io.Reader) (string, error) {
	// Four bytes per code point plus CRLF bounds input without trimming passwords.
	const limit = account.PasswordMaxLength*utf8.UTFMax + 2
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, limit), limit)
	// Scan can return buffered text despite a read error; reject that text.
	if !scanner.Scan() || scanner.Err() != nil {
		return "", errors.New("could not read recovery input; supply one line of at most 128 characters")
	}
	return scanner.Text(), nil
}
