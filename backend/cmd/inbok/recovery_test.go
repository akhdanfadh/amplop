package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"inbok/internal/account"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recoveryService struct {
	email, password     string
	findErr, recoverErr error
}

func (f *recoveryService) Administrator(_ context.Context, email string) (account.User, error) {
	f.email = email
	return account.User{Email: "admin@example.com", Role: account.RoleAdmin}, f.findErr
}
func (f *recoveryService) Recover(_ context.Context, email, password string) (account.RecoveryResult, error) {
	f.email, f.password = email, password
	return account.RecoveryResult{Factors: 1, BackupCodes: 4, Sessions: 2, Challenges: 3, TOTPAttempts: 20, SignInAttempts: 5}, f.recoverErr
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
	assert.Contains(t, output.String(), "Security page")
	assert.NotContains(t, output.String(), service.password)
	assert.NotContains(t, output.String(), "Continue?")
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
