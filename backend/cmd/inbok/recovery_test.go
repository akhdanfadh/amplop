package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"inbok/internal/account"
	"inbok/internal/emailaddress"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recoveryService struct {
	email, password     string
	findErr, recoverErr error
	blocker             account.NoticeOutcome
	blockerErr          error
}

func (f *recoveryService) NoticeBlocker(context.Context) (account.NoticeOutcome, error) {
	return f.blocker, f.blockerErr
}

func (f *recoveryService) Administrator(_ context.Context, email string) (account.User, error) {
	f.email = email
	return account.User{Email: "admin@example.com", Role: account.RoleAdmin}, f.findErr
}
func (f *recoveryService) Recover(_ context.Context, email, password string) (account.RecoveryResult, error) {
	f.email, f.password = email, password
	return account.RecoveryResult{Factors: 1, BackupCodes: 4, Sessions: 2, Challenges: 3, TOTPAttempts: 20, SignInAttempts: 5, ResetLinks: 6}, f.recoverErr
}

func TestRecoveryStdin(t *testing.T) {
	service := &recoveryService{}
	var output bytes.Buffer
	console := recoveryConsole{input: strings.NewReader("  a recovery password  \nignored line\n"), output: &output}

	err := runRecovery(t.Context(), "admin@example.com", service, console)

	require.NoError(t, err)
	assert.Equal(t, "  a recovery password  ", service.password)
	assert.Contains(t, output.String(), "4 backup codes removed")
	assert.Contains(t, output.String(), "2 sessions ended")
	assert.Contains(t, output.String(), "6 password reset links cleared")
	assert.Contains(t, output.String(), "set up TOTP")
	assert.NotContains(t, output.String(), service.password)
	assert.NotContains(t, output.String(), "Continue?")
	assert.Contains(t, output.String(), "Every active administrator will be emailed about this recovery")
}

// After a recovery the operator learns whether the administrators will be
// emailed, and why not; recovery stands either way.
func TestRecoveryNoticeLine(t *testing.T) {
	cases := map[string]struct {
		blocker account.NoticeOutcome
		err     error
		want    string
	}{
		"not configured": {blocker: account.NoticeNotConfigured, want: "System email is not configured; the administrators will be emailed only if it is set up and tested before the worker tries."},
		"not tested":     {blocker: account.NoticeNotTested, want: "System email has not passed a test message; the administrators will be emailed only if one passes before the worker tries."},
		"unknown":        {err: errors.New("connection lost"), want: "Could not check whether the administrators will be emailed."},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			service := &recoveryService{blocker: c.blocker, blockerErr: c.err}
			var output bytes.Buffer
			console := recoveryConsole{input: strings.NewReader("a recovery password\n"), output: &output}

			err := runRecovery(t.Context(), "admin@example.com", service, console)
			require.NoError(t, err)

			assert.Contains(t, output.String(), c.want)
			assert.NotContains(t, output.String(), "connection lost")
		})
	}
}

func TestRecoveryTerminal(t *testing.T) {
	for _, c := range []struct {
		name, repeat, confirmation string
		confirmed                  bool
	}{
		{"yes", "a recovery password", "y\n", true},
		{"yes word", "a recovery password", "YES\n", true},
		{"default no", "a recovery password", "\n", false},
		{"explicit no", "a recovery password", "n\n", false},
		{"missing confirmation", "a recovery password", "", false},
		{"mismatch", "another password", "y\n", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			service := &recoveryService{}
			var output bytes.Buffer
			passwords := []string{"a recovery password", c.repeat}
			console := recoveryConsole{input: strings.NewReader(c.confirmation), output: &output, readPassword: func() ([]byte, error) {
				password := passwords[0]
				passwords = passwords[1:]
				return []byte(password), nil
			}}

			err := runRecovery(t.Context(), "admin@example.com", service, console)

			if c.confirmed {
				require.NoError(t, err)
				assert.Equal(t, "a recovery password", service.password)
			} else {
				require.Error(t, err)
				assert.Empty(t, service.password)
			}
			assert.NotContains(t, output.String(), "a recovery password")
			assert.NotContains(t, output.String(), "another password")
			if c.repeat == "a recovery password" {
				assert.Contains(t, output.String(), "Continue? [y/N]")
			}
		})
	}
}

func TestRecoveryFailures(t *testing.T) {
	const secret = "private-password-and-hash"
	for _, c := range []struct {
		name, input         string
		findErr, recoverErr error
	}{
		{name: "missing", input: secret, findErr: account.ErrUserNotFound},
		{name: "member", input: secret, findErr: account.ErrRecoveryForbidden},
		{name: "lookup failure", input: secret, findErr: errors.New(secret)},
		{name: "write failure", input: secret, recoverErr: errors.New(secret)},
		{name: "invalid password", input: secret, recoverErr: account.ErrPasswordInvalid},
		{name: "empty stdin"},
		{name: "too much input", input: strings.Repeat(secret, 100)},
	} {
		t.Run(c.name, func(t *testing.T) {
			service := &recoveryService{findErr: c.findErr, recoverErr: c.recoverErr}
			var output, logs bytes.Buffer
			console := recoveryConsole{input: strings.NewReader(c.input), output: &output}

			err := runRecovery(t.Context(), "admin@example.com", service, console)

			require.Error(t, err)
			slog.New(slog.NewTextHandler(&logs, nil)).Error("inbok failed", "err", err)
			assert.NotContains(t, err.Error(), secret)
			assert.NotContains(t, output.String(), secret)
			assert.NotContains(t, logs.String(), secret)
			assert.NotContains(t, output.String(), "Password replaced")
			if c.findErr != nil {
				assert.Empty(t, service.password)
			}
		})
	}
}

// An invalid address is reported as such, not as an unconfirmed failure.
func TestRecoveryInvalidEmail(t *testing.T) {
	service := &recoveryService{findErr: emailaddress.ErrInvalid}
	console := recoveryConsole{input: strings.NewReader("a new recovery password"), output: &bytes.Buffer{}}

	err := runRecovery(t.Context(), "admin", service, console)

	require.ErrorIs(t, err, emailaddress.ErrInvalid)
	assert.Empty(t, service.password)
}

func TestRecoveryLineEndings(t *testing.T) {
	for _, ending := range []string{"", "\n", "\r\n"} {
		service := &recoveryService{}
		var output bytes.Buffer

		err := runRecovery(t.Context(), "admin@example.com", service, recoveryConsole{input: strings.NewReader(" a recovery password " + ending), output: &output})

		require.NoError(t, err)
		assert.Equal(t, " a recovery password ", service.password)
	}
}

type recoveryReadError struct {
	input string
}

func (r recoveryReadError) Read(p []byte) (int, error) {
	return copy(p, r.input), errors.New("private-read-error")
}

func TestRecoveryReadErrors(t *testing.T) {
	for _, readPassword := range []func() ([]byte, error){nil, func() ([]byte, error) { return nil, errors.New("private-read-error") }} {
		service := &recoveryService{}
		var output bytes.Buffer
		console := recoveryConsole{input: recoveryReadError{}, output: &output, readPassword: readPassword}

		err := runRecovery(t.Context(), "admin@example.com", service, console)

		require.Error(t, err)
		assert.Empty(t, service.password)
		assert.NotContains(t, err.Error(), "private-read-error")
		assert.NotContains(t, output.String(), "private-read-error")
	}
}

func TestRecoveryNoURL(t *testing.T) {
	t.Setenv("INBOK_DATABASE_URL", "")
	t.Setenv("INBOK_SETUP_TOKEN", "short")
	t.Setenv("INBOK_ENCRYPTION_KEYS", "invalid")

	err := run(t.Context(), []string{"recover-admin", "admin@example.com"}, discard)

	require.ErrorContains(t, err, "INBOK_DATABASE_URL")
}

func TestRecoveryUsage(t *testing.T) {
	for _, args := range [][]string{{"recover-admin"}, {"recover-admin", "admin@example.com", "private-argument"}} {
		err := run(t.Context(), args, discard)

		require.ErrorIs(t, err, errUsage)
		assert.NotContains(t, err.Error(), "private-argument")
	}
}

func TestRecoveryInterrupted(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	service := &recoveryService{}
	var output bytes.Buffer
	console := recoveryConsole{input: strings.NewReader("y\n"), output: &output, readPassword: func() ([]byte, error) { cancel(); return []byte("a recovery password"), nil }}

	err := runRecovery(ctx, "admin@example.com", service, console)

	require.Error(t, err)
	assert.Empty(t, service.password)
}

func TestRecoveryPartialRead(t *testing.T) {
	const password = "private password prefix"
	for _, c := range []struct {
		name, input  string
		readPassword func() ([]byte, error)
	}{
		{name: "password", input: password},
		{name: "confirmation", input: "y", readPassword: func() ([]byte, error) { return []byte(password), nil }},
	} {
		t.Run(c.name, func(t *testing.T) {
			service := &recoveryService{}
			var output, logs bytes.Buffer
			console := recoveryConsole{input: recoveryReadError{input: c.input}, output: &output, readPassword: c.readPassword}

			err := runRecovery(t.Context(), "admin@example.com", service, console)

			require.Error(t, err)
			assert.Empty(t, service.password, "an input failure must not reach recovery")
			slog.New(slog.NewTextHandler(&logs, nil)).Error("inbok failed", "err", err)
			for _, text := range []string{err.Error(), output.String(), logs.String()} {
				assert.NotContains(t, text, password)
				assert.NotContains(t, text, "private-read-error")
			}
			assert.NotContains(t, output.String(), "Password replaced")
		})
	}
}
