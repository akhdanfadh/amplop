package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"inbok/internal/httpapi"
)

var discard = slog.New(slog.DiscardHandler)

func TestExport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "openapi.json")
	if err := run(context.Background(), []string{"export", path}, discard); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	want, err := httpapi.New(discard, nil).OpenAPI()
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(got, want) {
		t.Fatalf("exported file differs from the API document:\n%s", got)
	}
}

func TestRunUsage(t *testing.T) {
	cases := [][]string{nil, {"unknown"}, {"serve", "extra"}, {"migrate", "extra"}, {"export"}, {"export", "a", "b"}}
	for _, args := range cases {
		if err := run(context.Background(), args, discard); !errors.Is(err, errUsage) {
			t.Fatalf("run(%q) = %v, want usage error", args, err)
		}
	}
}

// migrate names the missing setting instead of guessing a database.
func TestMigrateNoURL(t *testing.T) {
	t.Setenv("INBOK_DATABASE_URL", "")

	err := run(context.Background(), []string{"migrate"}, discard)
	if err == nil || !strings.Contains(err.Error(), "INBOK_DATABASE_URL") {
		t.Fatalf("err = %v, want an error naming INBOK_DATABASE_URL", err)
	}
}

// serve names the missing setting before touching a database or a port.
func TestServeNoURL(t *testing.T) {
	t.Setenv("INBOK_DATABASE_URL", "")

	err := run(context.Background(), []string{"serve"}, discard)
	if err == nil || !strings.Contains(err.Error(), "INBOK_DATABASE_URL") {
		t.Fatalf("err = %v, want an error naming INBOK_DATABASE_URL", err)
	}
}

// Cancelling the context, as SIGINT or SIGTERM does, stops the server
// cleanly.
func TestListenShutdown(t *testing.T) {
	addr := freeAddr(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- listen(ctx, addr, httpapi.New(discard, nil).Handler(), discard) }()

	url := "http://" + addr + "/api/v0/health"
	if !waitUp(url) {
		t.Fatal("server did not start")
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("listen = %v, want nil", err)
		}
	case <-time.After(shutdownTimeout + time.Second):
		t.Fatal("listen did not return after cancel")
	}

	if _, err := http.Get(url); err == nil {
		t.Fatal("server still accepts requests after shutdown")
	}
}

// A busy port fails listen without claiming the server started.
func TestListenBusyPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	var out bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&out, nil))
	err = listen(context.Background(), listener.Addr().String(), httpapi.New(discard, nil).Handler(), logger)
	if err == nil {
		t.Fatal("listen on a busy port must fail")
	}

	if out.Len() != 0 {
		t.Fatalf("listen logged before failing:\n%s", out.String())
	}
}

// freeAddr returns a loopback address with a port nothing listens on.
func freeAddr(t *testing.T) string {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
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
