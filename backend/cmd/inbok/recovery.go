package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"

	"inbok/internal/account"
	"inbok/internal/config"
	"inbok/internal/emailaddress"
	"inbok/internal/repository/postgres"
)

type adminRecovery interface {
	Administrator(context.Context, string) (account.User, error)
	Recover(context.Context, string, string) (account.RecoveryResult, error)
}

type recoveryConsole struct {
	input        io.Reader
	output       io.Writer
	readPassword func() ([]byte, error)
}

// runRecovery keeps input and output at the command boundary, outside account rules.
func runRecovery(ctx context.Context, email string, service adminRecovery, console recoveryConsole) error {
	user, err := service.Administrator(ctx, email)
	if err != nil {
		return recoveryError(err)
	}
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

	result, err := service.Recover(ctx, email, password)
	if err != nil {
		return recoveryError(err)
	}
	_, err = fmt.Fprintf(console.output, "Password replaced. %d TOTP factors and %d backup codes removed. %d sessions ended.\n%d sign-in challenges, %d TOTP attempts, %d sign-in attempts, and %d password reset links cleared.\nSign in, then set up TOTP on the Security page to restore administrator access.\n", result.Factors, result.BackupCodes, result.Sessions, result.Challenges, result.TOTPAttempts, result.SignInAttempts, result.ResetLinks)
	if err != nil {
		return errors.New("recovery completed, but could not write the summary")
	}
	return nil
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
	url, err := config.DatabaseURL()
	if err != nil {
		return err
	}
	db, err := postgres.Open(ctx, url)
	if err != nil {
		return errors.New("cannot connect to database for recovery")
	}
	defer db.Close()

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
	done := make(chan error, 1)
	go func() { done <- runRecovery(ctx, email, account.NewRecovery(db), console) }()
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
