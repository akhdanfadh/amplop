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

	want, err := httpapi.New(discard).OpenAPI()
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(got, want) {
		t.Fatalf("exported file differs from the API document:\n%s", got)
	}
}

func TestRunUsage(t *testing.T) {
	cases := [][]string{nil, {"unknown"}, {"serve", "extra"}, {"export"}, {"export", "a", "b"}}
	for _, args := range cases {
		if err := run(context.Background(), args, discard); !errors.Is(err, errUsage) {
			t.Fatalf("run(%q) = %v, want usage error", args, err)
		}
	}
}

// Cancelling the context, as SIGINT or SIGTERM does, stops serve cleanly.
func TestServeShutdown(t *testing.T) {
	addr := freeAddr(t)
	t.Setenv("INBOK_HTTP_ADDR", addr)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, []string{"serve"}, discard) }()

	url := "http://" + addr + "/api/v0/health"
	if !waitUp(url) {
		t.Fatal("server did not start")
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve = %v, want nil", err)
		}
	case <-time.After(shutdownTimeout + time.Second):
		t.Fatal("serve did not return after cancel")
	}

	if _, err := http.Get(url); err == nil {
		t.Fatal("server still accepts requests after shutdown")
	}
}

// A busy port fails serve without claiming the server started.
func TestServeBusyPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	t.Setenv("INBOK_HTTP_ADDR", listener.Addr().String())

	var out bytes.Buffer
	err = run(context.Background(), []string{"serve"}, slog.New(slog.NewTextHandler(&out, nil)))
	if err == nil {
		t.Fatal("serve on a busy port must fail")
	}

	if out.Len() != 0 {
		t.Fatalf("serve logged before failing:\n%s", out.String())
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
