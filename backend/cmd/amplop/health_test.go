package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// healthServer starts a server on IPv4 loopback answering the health path
// with status, and returns its port. Other paths answer 404.
func healthServer(t *testing.T, status int) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v0/health" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(server.Close)
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	require.NoError(t, err)
	return port
}

// noSecrets clears every setting serve needs but health does not.
func noSecrets(t *testing.T) {
	t.Helper()
	t.Setenv("AMPLOP_DATABASE_URL", "")
	t.Setenv("AMPLOP_ENCRYPTION_KEYS", "")
	t.Setenv("AMPLOP_SETUP_TOKEN", "")
}

// A server answering 200 on the configured address is healthy. An
// all-interfaces address reaches the server on IPv4 loopback, also [::].
func TestHealth(t *testing.T) {
	hosts := map[string]string{
		"loopback":            "127.0.0.1",
		"all IPv4 interfaces": "0.0.0.0",
		"all IPv6 interfaces": "::",
		"empty host":          "",
	}
	for name, host := range hosts {
		t.Run(name, func(t *testing.T) {
			noSecrets(t)
			t.Setenv("AMPLOP_HTTP_ADDR", net.JoinHostPort(host, healthServer(t, http.StatusOK)))

			err := run(context.Background(), []string{"health"}, discard)

			require.NoError(t, err)
		})
	}
}

// A server that answers anything but 200 is unhealthy, a redirect too.
func TestHealthStatus(t *testing.T) {
	for _, status := range []int{http.StatusMovedPermanently, http.StatusNotFound, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			noSecrets(t)
			t.Setenv("AMPLOP_HTTP_ADDR", net.JoinHostPort("127.0.0.1", healthServer(t, status)))

			err := run(context.Background(), []string{"health"}, discard)

			require.Error(t, err)
		})
	}
}

// Nothing listening is unhealthy.
func TestHealthDown(t *testing.T) {
	noSecrets(t)
	t.Setenv("AMPLOP_HTTP_ADDR", freeAddr(t))

	err := run(context.Background(), []string{"health"}, discard)

	require.Error(t, err)
}

// A server that never answers is unhealthy once the check's own time is
// up, with no deadline from the caller.
func TestHealthHung(t *testing.T) {
	// Given a server that holds every request until the test ends.
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })
	noSecrets(t)
	t.Setenv("AMPLOP_HTTP_ADDR", server.Listener.Addr().String())

	// When the check runs with a short limit.
	start := time.Now()
	err := health(context.Background(), 100*time.Millisecond)

	// Then it fails at that limit instead of waiting.
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(start), 2*time.Second)
}

// The image's check uses a limit below the HEALTHCHECK's 5-second timeout.
func TestHealthTimeout(t *testing.T) {
	require.Equal(t, 3*time.Second, healthTimeout)
}

// An invalid address names its setting.
func TestHealthBadAddr(t *testing.T) {
	noSecrets(t)
	t.Setenv("AMPLOP_HTTP_ADDR", "localhost")

	err := run(context.Background(), []string{"health"}, discard)

	require.ErrorContains(t, err, "AMPLOP_HTTP_ADDR")
}

// The worker's health handler answers what amplop health asks, so the
// image's health check works for the worker too.
func TestWorkerHealth(t *testing.T) {
	server := httptest.NewServer(workerHealth())
	t.Cleanup(server.Close)
	noSecrets(t)
	t.Setenv("AMPLOP_HTTP_ADDR", server.Listener.Addr().String())

	err := run(context.Background(), []string{"health"}, discard)

	require.NoError(t, err)
}
