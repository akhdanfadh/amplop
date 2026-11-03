// Package config reads process settings from environment variables.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/idna"

	"inbok/internal/encryption"
)

const (
	envHTTPAddr     = "INBOK_HTTP_ADDR"
	defaultHTTPAddr = "127.0.0.1:8080"

	// The worker's default differs from serve's, so both run on one
	// machine without settings.
	defaultWorkerHTTPAddr = "127.0.0.1:8081"

	envEncryptionKeys = "INBOK_ENCRYPTION_KEYS"

	envDatabaseURL    = "INBOK_DATABASE_URL"
	envTrustedProxies = "INBOK_TRUSTED_PROXIES"

	envSetupToken       = "INBOK_SETUP_TOKEN"
	minSetupTokenLength = 32

	envPublicURL = "INBOK_PUBLIC_URL"

	envWorkerID     = "INBOK_WORKER_ID"
	defaultWorkerID = "worker"
	maxWorkerID     = 64
)

// errNoDatabaseURL has no default on purpose: a server deployed without
// the setting must stop, not try a local database.
var errNoDatabaseURL = errors.New(envDatabaseURL + " is required, e.g. postgres://user:password@127.0.0.1:5432/inbok")

// Errors for an unusable public URL. None includes the value.
var (
	errNoPublicURL     = errors.New(envPublicURL + " is required: the address users open inbok at, e.g. https://mail.example.com")
	errPublicURLScheme = errors.New(envPublicURL + " must use https, or http only for localhost and loopback addresses")
	errPublicURLShape  = errors.New(envPublicURL + " must be only a scheme, a host name or IP address, and an optional port, e.g. https://mail.example.com")
)

// Config holds settings for inbok serve.
type Config struct {
	Encryption     *encryption.Keyring // validated keys for stored secrets
	HTTPAddr       string              // host:port to listen on, e.g. 0.0.0.0:8080
	PublicURL      *url.URL            // where users open inbok: scheme, host, and port only
	SetupToken     string              // empty disables first-admin setup
	TrustedProxies []string            // CIDRs; empty ignores forwarded client addresses
}

// Load reads the environment. Encryption keys are required; optional settings use defaults.
func Load() (Config, error) {
	addr, err := HTTPAddr()
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		HTTPAddr:   addr,
		SetupToken: os.Getenv(envSetupToken),
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

	publicURL, err := parsePublicURL(os.Getenv(envPublicURL))
	if err != nil {
		return Config{}, err
	}
	cfg.PublicURL = publicURL

	value := os.Getenv(envEncryptionKeys)
	if strings.TrimSpace(value) == "" {
		return Config{}, fmt.Errorf("%s is required", envEncryptionKeys)
	}

	keys, err := encryption.New(value)
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", envEncryptionKeys, err)
	}
	cfg.Encryption = keys

	return cfg, nil
}

// HTTPAddr reads the host:port serve listens on, or its default. Returns an
// error naming the setting if the value is not host:port.
func HTTPAddr() (string, error) {
	return httpAddr(defaultHTTPAddr)
}

// WorkerHTTPAddr reads the host:port the worker answers health checks on,
// or the worker's default. Returns an error naming the setting if the
// value is not host:port.
func WorkerHTTPAddr() (string, error) {
	return httpAddr(defaultWorkerHTTPAddr)
}

// httpAddr reads INBOK_HTTP_ADDR, or returns fallback when it is empty.
// Returns an error naming the setting if the value is not host:port.
func httpAddr(fallback string) (string, error) {
	addr := os.Getenv(envHTTPAddr)
	if addr == "" {
		addr = fallback
	}
	// Fail with the variable name, not later at listen or dial time.
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return "", fmt.Errorf("%s: %w", envHTTPAddr, err)
	}
	return addr, nil
}

// DatabaseURL reads the PostgreSQL connection URL, which has no default.
func DatabaseURL() (string, error) {
	url := os.Getenv(envDatabaseURL)
	if url == "" {
		return "", errNoDatabaseURL
	}
	return url, nil
}

// errWorkerID does not include the value.
var errWorkerID = fmt.Errorf("%s must be 1 to %d letters, digits, dots, hyphens, or underscores", envWorkerID, maxWorkerID)

// WorkerID reads the name the worker resumes its unfinished work under, or
// its default. Returns an error naming the setting if the value is too
// long or has other characters.
func WorkerID() (string, error) {
	id := os.Getenv(envWorkerID)
	if id == "" {
		return defaultWorkerID, nil
	}
	if len(id) > maxWorkerID || strings.IndexFunc(id, notWorkerIDRune) >= 0 {
		return "", errWorkerID
	}
	return id, nil
}

// notWorkerIDRune reports whether r is not allowed in a worker ID.
func notWorkerIDRune(r rune) bool {
	switch {
	case 'a' <= r && r <= 'z', 'A' <= r && r <= 'Z', '0' <= r && r <= '9':
		return false
	case r == '.', r == '-', r == '_':
		return false
	}
	return true
}

// parsePublicURL returns value as a URL with only a scheme, host, and
// optional port. It returns an error naming INBOK_PUBLIC_URL when value is
// empty or is not such an origin.
func parsePublicURL(value string) (*url.URL, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, errNoPublicURL
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return nil, errPublicURLShape
	}
	// Links and the EHLO name use one spelling of the host, so the loopback
	// check below reads the same one.
	host, hostOK := publicHost(parsed.Hostname())
	switch {
	case parsed.Scheme == "https":
	case parsed.Scheme == "http" && hostOK && isLoopback(host):
	default:
		return nil, errPublicURLScheme
	}
	// Links are built by adding a path and a fragment, so the text must be the
	// origin alone, apart from one trailing slash. Comparing the whole text
	// also catches what url.URL does not keep, e.g. an empty "#".
	if !hostOK || !strings.EqualFold(strings.TrimSuffix(value, "/"), parsed.Scheme+"://"+parsed.Host) {
		return nil, errPublicURLShape
	}
	port := parsed.Port()
	if port != "" || strings.HasSuffix(parsed.Host, ":") {
		number, err := strconv.ParseUint(port, 10, 16)
		if err != nil || number == 0 {
			return nil, errPublicURLShape
		}
		// Written back as a number, :0443 becomes :443.
		port = strconv.FormatUint(number, 10)
	}
	// Rebuild the host from its standard spelling; an IPv6 address needs its
	// brackets back.
	switch {
	case port != "":
		host = net.JoinHostPort(host, port)
	case strings.Contains(host, ":"):
		host = "[" + host + "]"
	}
	return &url.URL{Scheme: parsed.Scheme, Host: host}, nil
}

// hostNames converts a domain to lowercase ASCII with the IDNA2008 lookup
// rules (RFC 5891). It rejects labels other than letters, digits, and inner
// hyphens, labels outside 1 to 63 characters, and names over 253.
var hostNames = idna.New(idna.MapForLookup(), idna.StrictDomainName(true), idna.VerifyDNSLength(true))

// publicHost returns host in its standard spelling: an IP address as netip
// writes it, a domain in lowercase ASCII. It reports false for an IP address
// with a zone, an unspecified address, or an invalid domain.
func publicHost(host string) (string, bool) {
	if ip, err := netip.ParseAddr(host); err == nil {
		// A zone means something only on this machine, and an unspecified
		// address names no host.
		return ip.String(), ip.Zone() == "" && !ip.IsUnspecified()
	}
	// The host is also the SMTP client's EHLO name (RFC 5321 section 4.1.2), and
	// a mail client's link detection stops at characters a URL host allows.
	ascii, err := hostNames.ToASCII(host)
	return ascii, err == nil
}

// isLoopback reports whether host, as [publicHost] returns it, is localhost or
// a loopback IP address.
func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}
