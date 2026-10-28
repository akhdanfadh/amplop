// Package config reads process settings from environment variables.
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"unicode/utf8"
)

const (
	envHTTPAddr     = "INBOK_HTTP_ADDR"
	defaultHTTPAddr = "127.0.0.1:8080"

	envDatabaseURL    = "INBOK_DATABASE_URL"
	envTrustedProxies = "INBOK_TRUSTED_PROXIES"

	envSetupToken       = "INBOK_SETUP_TOKEN"
	minSetupTokenLength = 32
)

// errNoDatabaseURL has no default on purpose: a server deployed without
// the setting must stop, not try a local database.
var errNoDatabaseURL = errors.New(envDatabaseURL + " is required, e.g. postgres://user:password@127.0.0.1:5432/inbok")

// Config holds settings for inbok serve.
type Config struct {
	HTTPAddr       string   // host:port to listen on, e.g. 0.0.0.0:8080
	SetupToken     string   // empty disables first-admin setup
	TrustedProxies []string // CIDRs; empty ignores forwarded client addresses
}

// Load reads the environment. Unset or empty variables use defaults.
func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:   os.Getenv(envHTTPAddr),
		SetupToken: os.Getenv(envSetupToken),
	}
	if cfg.HTTPAddr == "" {
		cfg.HTTPAddr = defaultHTTPAddr
	}

	// Fail at startup with the variable name, not later at listen time.
	if _, _, err := net.SplitHostPort(cfg.HTTPAddr); err != nil {
		return Config{}, fmt.Errorf("%s: %w", envHTTPAddr, err)
	}

	// Count characters, not bytes, and keep the token out of errors.
	if cfg.SetupToken != "" && utf8.RuneCountInString(cfg.SetupToken) < minSetupTokenLength {
		return Config{}, fmt.Errorf("%s must be at least %d characters", envSetupToken, minSetupTokenLength)
	}

	if value := strings.TrimSpace(os.Getenv(envTrustedProxies)); value != "" {
		for entry := range strings.SplitSeq(value, ",") {
			cidr := strings.TrimSpace(entry)
			if _, _, err := net.ParseCIDR(cidr); err != nil {
				return Config{}, fmt.Errorf("%s must be a comma-separated list of CIDRs", envTrustedProxies)
			}
			cfg.TrustedProxies = append(cfg.TrustedProxies, cidr)
		}
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
