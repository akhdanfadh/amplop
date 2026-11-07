package main

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/httpapi"
)

var discard = slog.New(slog.DiscardHandler)

func TestExport(t *testing.T) {
	t.Setenv("INBOK_ENCRYPTION_KEYS", "invalid")
	t.Setenv("INBOK_TRUSTED_PROXIES", "invalid")
	path := filepath.Join(t.TempDir(), "openapi.json")
	require.NoError(t, run(context.Background(), []string{"export", path}, discard))

	got, err := os.ReadFile(path)
	require.NoError(t, err)

	want, err := httpapi.New(discard, httpapi.Services{}).OpenAPI()
	require.NoError(t, err)

	// Strings, not bytes: a mismatch then prints as a text diff.
	assert.Equal(t, string(want), string(got))
}

func TestRunUsage(t *testing.T) {
	cases := [][]string{nil, {"unknown"}, {"serve", "extra"}, {"migrate", "extra"}, {"health", "extra"}, {"worker", "extra"}, {"export"}, {"export", "a", "b"}}
	for _, args := range cases {
		err := run(context.Background(), args, discard)
		require.ErrorIs(t, err, errUsage, "run(%q)", args)
	}
}

// migrate names the missing setting instead of guessing a database.
func TestMigrateNoURL(t *testing.T) {
	t.Setenv("INBOK_ENCRYPTION_KEYS", "invalid")
	t.Setenv("INBOK_DATABASE_URL", "")

	err := run(context.Background(), []string{"migrate"}, discard)

	require.ErrorContains(t, err, "INBOK_DATABASE_URL")
}

// serve names the missing setting before touching a database or a port.
func TestServeNoURL(t *testing.T) {
	t.Setenv("INBOK_ENCRYPTION_KEYS", "1:AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=")
	t.Setenv("INBOK_PUBLIC_URL", "https://mail.example.com")
	t.Setenv("INBOK_S3_ENDPOINT", "http://127.0.0.1:1")
	t.Setenv("INBOK_S3_ACCESS_KEY_ID", "test-key-id")
	t.Setenv("INBOK_S3_SECRET_ACCESS_KEY", "test-secret-access-key")
	t.Setenv("INBOK_DATABASE_URL", "")
	t.Setenv("INBOK_SETUP_TOKEN", "")
	t.Setenv("INBOK_TRUSTED_PROXIES", "")

	err := run(context.Background(), []string{"serve"}, discard)

	require.ErrorContains(t, err, "INBOK_DATABASE_URL")
}

// Bad proxy settings stop serve before opening a database or listener.
func TestServeBadTrustedProxies(t *testing.T) {
	t.Setenv("INBOK_HTTP_ADDR", "")
	t.Setenv("INBOK_DATABASE_URL", "")
	t.Setenv("INBOK_SETUP_TOKEN", "")
	t.Setenv("INBOK_TRUSTED_PROXIES", "invalid")

	err := run(context.Background(), []string{"serve"}, discard)

	require.ErrorContains(t, err, "INBOK_TRUSTED_PROXIES")
}

// An invalid setup token stops serve before it needs a database or a port.
func TestServeShortSetupToken(t *testing.T) {
	const token = "short-test-setup-token"
	t.Setenv("INBOK_HTTP_ADDR", "")
	t.Setenv("INBOK_DATABASE_URL", "")
	t.Setenv("INBOK_SETUP_TOKEN", token)

	err := run(context.Background(), []string{"serve"}, discard)

	require.ErrorContains(t, err, "INBOK_SETUP_TOKEN")
	assert.NotContains(t, err.Error(), token)
}

// Cancelling the context, as SIGINT or SIGTERM does, stops the server
// cleanly.
func TestListenShutdown(t *testing.T) {
	// Given a server started in the background and answering.
	addr := freeAddr(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- listen(ctx, addr, httpapi.New(discard, httpapi.Services{}).Handler(), discard)
	}()

	url := "http://" + addr + "/api/v0/health"
	require.True(t, waitUp(url), "server did not start")

	// When its context is cancelled.
	cancel()

	// Then listen returns without error in time, and the port closes.
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(shutdownTimeout + time.Second):
		require.FailNow(t, "listen did not return after cancel")
	}

	_, err := http.Get(url)
	require.Error(t, err, "server still accepts requests after shutdown")
}

// A busy port fails listen without claiming the server started.
func TestListenBusyPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	var out bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&out, nil))
	err = listen(context.Background(), listener.Addr().String(), httpapi.New(discard, httpapi.Services{}).Handler(), logger)
	require.Error(t, err)
	assert.Empty(t, out.String(), "listen logged before failing")
}

// freeAddr returns a loopback address with a port nothing listens on.
func freeAddr(t *testing.T) string {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	return listener.Addr().String()
}

func waitUp(url string) bool {
	for range 50 {
		response, err := http.Get(url)
		if err == nil {
			response.Body.Close()
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// Missing or malformed keys stop serve before database or listener startup.
func TestServeEncryptionKeys(t *testing.T) {
	for _, value := range []string{"", "invalid-private-key"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("INBOK_HTTP_ADDR", "")
			t.Setenv("INBOK_SETUP_TOKEN", "")
			t.Setenv("INBOK_TRUSTED_PROXIES", "")
			t.Setenv("INBOK_PUBLIC_URL", "https://mail.example.com")
			t.Setenv("INBOK_DATABASE_URL", "")
			t.Setenv("INBOK_ENCRYPTION_KEYS", value)
			err := run(context.Background(), []string{"serve"}, discard)
			require.ErrorContains(t, err, "INBOK_ENCRYPTION_KEYS")
			if value != "" {
				assert.NotContains(t, err.Error(), value)
			}
		})
	}
}

// A missing or unusable public URL stops serve before database or listener
// startup.
func TestServePublicURL(t *testing.T) {
	for _, value := range []string{"", "http://mail.example.com", "https://mail.example.com/inbok"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("INBOK_HTTP_ADDR", "")
			t.Setenv("INBOK_SETUP_TOKEN", "")
			t.Setenv("INBOK_TRUSTED_PROXIES", "")
			t.Setenv("INBOK_DATABASE_URL", "")
			t.Setenv("INBOK_ENCRYPTION_KEYS", "1:AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=")
			t.Setenv("INBOK_PUBLIC_URL", value)

			err := run(context.Background(), []string{"serve"}, discard)
			require.ErrorContains(t, err, "INBOK_PUBLIC_URL")
		})
	}
}
