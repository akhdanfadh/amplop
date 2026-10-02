package config

import (
	"strings"
	"testing"
)

func TestLoadDefault(t *testing.T) {
	t.Setenv(envHTTPAddr, "")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	if cfg.HTTPAddr != "127.0.0.1:8080" {
		t.Fatalf("HTTPAddr = %q", cfg.HTTPAddr)
	}
}

func TestLoadAddr(t *testing.T) {
	t.Setenv(envHTTPAddr, "0.0.0.0:9090")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	if cfg.HTTPAddr != "0.0.0.0:9090" {
		t.Fatalf("HTTPAddr = %q", cfg.HTTPAddr)
	}
}

func TestLoadBadAddr(t *testing.T) {
	t.Setenv(envHTTPAddr, "localhost")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), envHTTPAddr) {
		t.Fatalf("err = %v, want an error naming %s", err, envHTTPAddr)
	}
}
