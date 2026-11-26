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

	"amplop/internal/encryption"
)

const (
	envHTTPAddr     = "AMPLOP_HTTP_ADDR"
	defaultHTTPAddr = "127.0.0.1:8080"

	// The worker's default differs from serve's, so both run on one
	// machine without settings.
	defaultWorkerHTTPAddr = "127.0.0.1:8081"

	envEncryptionKeys = "AMPLOP_ENCRYPTION_KEYS"

	envDatabaseURL    = "AMPLOP_DATABASE_URL"
	envTrustedProxies = "AMPLOP_TRUSTED_PROXIES"

	envSetupToken = "AMPLOP_SETUP_TOKEN"
	// minSetupTokenLength is amplop's own floor against a short, guessable
	// token; openssl rand -base64 32 gives 44 characters.
	minSetupTokenLength = 32

	envPublicURL = "AMPLOP_PUBLIC_URL"

	envSearchURL = "AMPLOP_MEILISEARCH_URL"
	envSearchKey = "AMPLOP_MEILISEARCH_KEY"

	envS3Endpoint        = "AMPLOP_S3_ENDPOINT"
	envS3AccessKeyID     = "AMPLOP_S3_ACCESS_KEY_ID"
	envS3SecretAccessKey = "AMPLOP_S3_SECRET_ACCESS_KEY"

	envWorkerID     = "AMPLOP_WORKER_ID"
	defaultWorkerID = "worker"
	// maxWorkerID and [notWorkerIDRune] are amplop's own limits: DBOS stores
	// any text as an executor ID.
	maxWorkerID = 64

	envGoogleClientID     = "AMPLOP_GOOGLE_CLIENT_ID"
	envGoogleClientSecret = "AMPLOP_GOOGLE_CLIENT_SECRET"
	envGoogleBaseURL      = "AMPLOP_GOOGLE_BASE_URL"
)

// errNoDatabaseURL has no default on purpose: a server deployed without
// the setting must stop, not try a local database.
var errNoDatabaseURL = errors.New(envDatabaseURL + " is required, e.g. postgres://user:password@127.0.0.1:5432/amplop")

// Errors for an unusable public URL. None includes the value.
var (
	errNoPublicURL     = errors.New(envPublicURL + " is required: the address users open amplop at, e.g. https://mail.example.com")
	errPublicURLScheme = errors.New(envPublicURL + " must use https, or http only for localhost and loopback addresses")
	errPublicURLShape  = errors.New(envPublicURL + " must be only a scheme, a host name or IP address, and an optional port, e.g. https://mail.example.com")
)

// Errors for an unusable object store endpoint. None includes the value.
var (
	errNoS3Endpoint    = errors.New(envS3Endpoint + " is required: the S3 service that holds message content, e.g. http://127.0.0.1:8333")
	errS3EndpointShape = errors.New(envS3Endpoint + " must be only http or https, a host, and an optional port, e.g. http://seaweedfs:8333")
)

// Errors for unusable Google settings. None includes a value.
var (
	errGoogleClientPair   = errors.New(envGoogleClientID + " and " + envGoogleClientSecret + " must be set together; leave both empty to not offer Gmail")
	errGoogleBaseNoClient = errors.New(envGoogleBaseURL + " needs " + envGoogleClientID + " and " + envGoogleClientSecret)
	errGoogleBaseURLHost  = errors.New(envGoogleBaseURL + " must be an http or https address on this machine, e.g. http://127.0.0.1:4000: it points amplop at a local emulator, never at another server")
	errGoogleBaseURLShape = errors.New(envGoogleBaseURL + " must be only a scheme, localhost or a loopback address, and an optional port, e.g. http://127.0.0.1:4000")
)

// Errors [parseOrigin] returns; each caller names its own setting instead.
var (
	errOriginScheme = errors.New("scheme not allowed")
	errOriginShape  = errors.New("not an origin")
)

// Config holds settings for amplop serve.
type Config struct {
	Encryption     *encryption.Keyring // validated keys for stored secrets
	Google         *Google             // nil: Gmail is not offered
	HTTPAddr       string              // host:port to listen on, e.g. 0.0.0.0:8080
	PublicURL      *url.URL            // where users open amplop: scheme, host, and port only
	S3             S3                  // where message content is kept
	SetupToken     string              // empty disables first-admin setup
	TrustedProxies []string            // CIDRs; empty ignores forwarded client addresses
}

// Google holds the OAuth client members connect Gmail accounts with.
type Google struct {
	ClientID     string
	ClientSecret string
	// BaseURL is one server on this machine that answers for Google, e.g.
	// the emulator, with the emulator's paths: scheme, host, and port only.
	// Nil means Google's own hosts.
	BaseURL *url.URL
}

// S3 names the S3-compatible service that holds message content and the
// keys amplop signs in with.
type S3 struct {
	Endpoint        *url.URL // scheme, host, and port only, e.g. http://seaweedfs:8333
	AccessKeyID     string
	SecretAccessKey string
}

// Load reads the environment. Encryption keys, the public URL, and the
// object store are required; optional settings use defaults.
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

	publicURL, err := PublicURL()
	if err != nil {
		return Config{}, err
	}
	cfg.PublicURL = publicURL

	google, err := GoogleSettings()
	if err != nil {
		return Config{}, err
	}
	cfg.Google = google

	keys, err := EncryptionKeys()
	if err != nil {
		return Config{}, err
	}
	cfg.Encryption = keys

	store, err := S3Settings()
	if err != nil {
		return Config{}, err
	}
	cfg.S3 = store

	return cfg, nil
}

// S3Settings reads the S3 service that holds message content, which has
// no default. Returns an error naming the setting, without its value, if
// one is missing or the endpoint is not an origin.
func S3Settings() (S3, error) {
	endpoint, err := parseS3Endpoint(os.Getenv(envS3Endpoint))
	if err != nil {
		return S3{}, err
	}
	store := S3{
		Endpoint:        endpoint,
		AccessKeyID:     os.Getenv(envS3AccessKeyID),
		SecretAccessKey: os.Getenv(envS3SecretAccessKey),
	}
	// Taken as they are otherwise: a space may belong to the key.
	if strings.TrimSpace(store.AccessKeyID) == "" {
		return S3{}, fmt.Errorf("%s is required", envS3AccessKeyID)
	}
	if strings.TrimSpace(store.SecretAccessKey) == "" {
		return S3{}, fmt.Errorf("%s is required", envS3SecretAccessKey)
	}
	return store, nil
}

// parseS3Endpoint returns value as a URL with only an http or https
// scheme, a host, and an optional port. It returns an error naming
// AMPLOP_S3_ENDPOINT when value is empty or is not such an origin.
func parseS3Endpoint(value string) (*url.URL, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, errNoS3Endpoint
	}
	// Plain http is for a store on a private network, as in Compose: SigV4
	// signs requests without sending the secret, but content travels
	// unencrypted. The SDK appends the bucket and key to the path
	// (path-style addressing), so the endpoint must be an origin alone.
	origin, err := parseOrigin(value, func(scheme string, _ bool) bool {
		return scheme == "http" || scheme == "https"
	})
	if err != nil {
		return nil, errS3EndpointShape
	}
	return origin, nil
}

// PublicURL reads the address users open amplop at, which has no default.
// Returns an error naming the setting if it is missing or not an origin.
func PublicURL() (*url.URL, error) {
	return parsePublicURL(os.Getenv(envPublicURL))
}

// EncryptionKeys reads the keys for stored secrets, which have no default.
// Returns an error naming the setting, without key material, if they are
// missing or malformed.
func EncryptionKeys() (*encryption.Keyring, error) {
	value := os.Getenv(envEncryptionKeys)
	if strings.TrimSpace(value) == "" {
		return nil, fmt.Errorf("%s is required", envEncryptionKeys)
	}
	keys, err := encryption.New(value)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", envEncryptionKeys, err)
	}
	return keys, nil
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

// httpAddr reads AMPLOP_HTTP_ADDR, or returns fallback when it is empty.
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

// Errors for unusable search index settings. None includes the value.
var (
	errNoSearchURL = errors.New(envSearchURL + " is required: where Meilisearch answers, e.g. http://127.0.0.1:7700")
	errSearchURL   = errors.New(envSearchURL + " must be http or https, a host, and an optional port, e.g. http://meilisearch:7700")
	errNoSearchKey = errors.New(envSearchKey + " is required: Meilisearch's master key")
)

// SearchIndex reads where Meilisearch answers and its master key, which
// have no defaults. Returns an error naming the setting, without the key,
// if either is missing or the URL is not a server's address.
func SearchIndex() (address, key string, err error) {
	address = strings.TrimSpace(os.Getenv(envSearchURL))
	if address == "" {
		return "", "", errNoSearchURL
	}
	// The client adds each API path to the address, so it must be the
	// server's origin alone, apart from one trailing slash.
	parsed, err := url.Parse(address)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
		strings.TrimSuffix(address, "/") != parsed.Scheme+"://"+parsed.Host {
		return "", "", errSearchURL
	}
	key = os.Getenv(envSearchKey)
	if key == "" {
		return "", "", errNoSearchKey
	}
	return strings.TrimSuffix(address, "/"), key, nil
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
// optional port. It returns an error naming AMPLOP_PUBLIC_URL when value is
// empty or is not such an origin.
func parsePublicURL(value string) (*url.URL, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, errNoPublicURL
	}
	// The session cookie is Secure, so a deployment needs https. Plain http on
	// localhost and loopback is for development; whether a browser keeps a
	// Secure cookie there depends on the browser.
	origin, err := parseOrigin(value, func(scheme string, loopback bool) bool {
		return scheme == "https" || (scheme == "http" && loopback)
	})
	switch {
	case errors.Is(err, errOriginScheme):
		return nil, errPublicURLScheme
	case err != nil:
		return nil, errPublicURLShape
	}
	return origin, nil
}

// GoogleSettings reads the Google OAuth client members connect Gmail
// accounts with. It returns nil when all three Google settings are empty,
// and an error naming the setting, without its value, when only one client
// setting is set, the base URL is set without a client, or the base URL is
// not an origin on this machine.
func GoogleSettings() (*Google, error) {
	// A value pasted from Google's console can carry a trailing newline.
	id := strings.TrimSpace(os.Getenv(envGoogleClientID))
	secret := strings.TrimSpace(os.Getenv(envGoogleClientSecret))
	base := strings.TrimSpace(os.Getenv(envGoogleBaseURL))
	switch {
	case id == "" && secret == "" && base == "":
		return nil, nil
	case id == "" && secret == "":
		return nil, errGoogleBaseNoClient
	case id == "" || secret == "":
		return nil, errGoogleClientPair
	}
	google := &Google{ClientID: id, ClientSecret: secret}
	if base == "" {
		return google, nil
	}
	// Only this machine: a mistyped or copied value must not send members'
	// authorization codes and refresh tokens to another server.
	origin, err := parseOrigin(base, func(scheme string, loopback bool) bool {
		return (scheme == "http" || scheme == "https") && loopback
	})
	switch {
	case errors.Is(err, errOriginScheme):
		return nil, errGoogleBaseURLHost
	case err != nil:
		return nil, errGoogleBaseURLShape
	}
	google.BaseURL = origin
	return google, nil
}

// parseOrigin returns value, a nonempty URL, with only a scheme, host, and
// optional port, in the host's standard spelling. schemeOK says whether the
// scheme fits the setting, given whether the host is localhost or a
// loopback address. It returns [errOriginScheme] when schemeOK refuses and
// [errOriginShape] when value is not such an origin.
func parseOrigin(value string, schemeOK func(scheme string, loopback bool) bool) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil {
		return nil, errOriginShape
	}
	// Links and the EHLO name use one spelling of the host, so the loopback
	// check below reads the same one.
	host, hostOK := publicHost(parsed.Hostname())
	if !schemeOK(parsed.Scheme, hostOK && isLoopback(host)) {
		return nil, errOriginScheme
	}
	// Links are built by adding a path and a fragment, so the text must be the
	// origin alone, apart from one trailing slash. Comparing the whole text
	// also catches what url.URL does not keep, e.g. an empty "#".
	if !hostOK || !strings.EqualFold(strings.TrimSuffix(value, "/"), parsed.Scheme+"://"+parsed.Host) {
		return nil, errOriginShape
	}
	port := parsed.Port()
	if port != "" || strings.HasSuffix(parsed.Host, ":") {
		// TCP ports are 16 bits, and port 0 is reserved (RFC 6335).
		number, err := strconv.ParseUint(port, 10, 16)
		if err != nil || number == 0 {
			return nil, errOriginShape
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
