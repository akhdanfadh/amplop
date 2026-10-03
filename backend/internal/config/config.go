// Package config reads process settings from environment variables.
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
)

const (
	envHTTPAddr     = "INBOK_HTTP_ADDR"
	defaultHTTPAddr = "127.0.0.1:8080"

	envDatabaseURL = "INBOK_DATABASE_URL"
)

// errNoDatabaseURL has no default on purpose: a server deployed without
// the setting must stop, not try a local database.
var errNoDatabaseURL = errors.New(envDatabaseURL + " is required, e.g. postgres://user:password@127.0.0.1:5432/inbok")

// Config holds settings for inbok serve.
type Config struct {
	HTTPAddr string // host:port to listen on, e.g. 0.0.0.0:8080
}

// Load reads the environment. Unset or empty variables use defaults.
func Load() (Config, error) {
	cfg := Config{HTTPAddr: os.Getenv(envHTTPAddr)}
	if cfg.HTTPAddr == "" {
		cfg.HTTPAddr = defaultHTTPAddr
	}

	// Fail at startup with the variable name, not later at listen time.
	if _, _, err := net.SplitHostPort(cfg.HTTPAddr); err != nil {
		return Config{}, fmt.Errorf("%s: %w", envHTTPAddr, err)
	}

	return cfg, nil
}

// DatabaseURL reads the PostgreSQL connection URL, which has no default.
func DatabaseURL() (string, error) {
	url := os.Getenv(envDatabaseURL)
	if url == "" {
		return "", errNoDatabaseURL
	}
	return url, nil
}
