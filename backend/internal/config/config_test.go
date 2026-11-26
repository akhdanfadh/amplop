package config

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testEncryptionKeys = "1:AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE="
	testPublicURL      = "https://mail.example.com"

	testS3Endpoint        = "http://seaweedfs:8333"
	testS3AccessKeyID     = "test-key-id"
	testS3SecretAccessKey = "test-secret-access-key"
)

// TestMain clears every inbok setting the shell passed in, e.g. from a
// sourced .env, so each test sees only what it sets.
func TestMain(m *testing.M) {
	for _, entry := range os.Environ() {
		if name, _, _ := strings.Cut(entry, "="); strings.HasPrefix(name, "INBOK_") {
			os.Unsetenv(name)
		}
	}
	os.Exit(m.Run())
}

// setS3 sets valid S3 settings for one test, so a test
// of another setting can call [Load].
func setS3(t *testing.T) {
	t.Helper()
	t.Setenv(envS3Endpoint, testS3Endpoint)
	t.Setenv(envS3AccessKeyID, testS3AccessKeyID)
	t.Setenv(envS3SecretAccessKey, testS3SecretAccessKey)
}

func TestLoadDefault(t *testing.T) {
	setS3(t)
	t.Setenv(envEncryptionKeys, testEncryptionKeys)
	t.Setenv(envPublicURL, testPublicURL)
	t.Setenv(envHTTPAddr, "")
	t.Setenv(envSetupToken, "")
	t.Setenv(envTrustedProxies, "")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "127.0.0.1:8080", cfg.HTTPAddr)
	assert.Empty(t, cfg.TrustedProxies)
}

func TestLoadAddr(t *testing.T) {
	setS3(t)
	t.Setenv(envEncryptionKeys, testEncryptionKeys)
	t.Setenv(envPublicURL, testPublicURL)
	t.Setenv(envHTTPAddr, "0.0.0.0:9090")
	t.Setenv(envSetupToken, "")
	t.Setenv(envTrustedProxies, "")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "0.0.0.0:9090", cfg.HTTPAddr)
}

func TestLoadBadAddr(t *testing.T) {
	setS3(t)
	t.Setenv(envEncryptionKeys, testEncryptionKeys)
	t.Setenv(envPublicURL, testPublicURL)
	t.Setenv(envHTTPAddr, "localhost")
	t.Setenv(envSetupToken, "")
	t.Setenv(envTrustedProxies, "")

	_, err := Load()

	require.ErrorContains(t, err, envHTTPAddr)
}

// HTTPAddr needs no other setting, e.g. no encryption keys.
func TestHTTPAddr(t *testing.T) {
	t.Setenv(envEncryptionKeys, "")
	cases := map[string]string{"": "127.0.0.1:8080", "0.0.0.0:9090": "0.0.0.0:9090"}
	for value, want := range cases {
		t.Run(value, func(t *testing.T) {
			t.Setenv(envHTTPAddr, value)

			got, err := HTTPAddr()
			require.NoError(t, err)

			assert.Equal(t, want, got)
		})
	}
}

// The worker's default differs from serve's, so both run on one machine;
// a set value applies to either.
func TestWorkerHTTPAddr(t *testing.T) {
	cases := map[string]string{"": "127.0.0.1:8081", "127.0.0.1:9091": "127.0.0.1:9091"}
	for value, want := range cases {
		t.Run(value, func(t *testing.T) {
			t.Setenv(envHTTPAddr, value)

			got, err := WorkerHTTPAddr()
			require.NoError(t, err)

			assert.Equal(t, want, got)
		})
	}
}

func TestWorkerHTTPAddrBad(t *testing.T) {
	t.Setenv(envHTTPAddr, "localhost")

	_, err := WorkerHTTPAddr()

	require.ErrorContains(t, err, envHTTPAddr)
}

func TestHTTPAddrBad(t *testing.T) {
	t.Setenv(envHTTPAddr, "localhost")

	_, err := HTTPAddr()

	require.ErrorContains(t, err, envHTTPAddr)
}

func TestLoadTrustedProxiesUnset(t *testing.T) {
	setS3(t)
	t.Setenv(envEncryptionKeys, testEncryptionKeys)
	t.Setenv(envPublicURL, testPublicURL)
	t.Setenv(envHTTPAddr, "")
	t.Setenv(envSetupToken, "")
	t.Setenv(envTrustedProxies, "")
	require.NoError(t, os.Unsetenv(envTrustedProxies))

	cfg, err := Load()
	require.NoError(t, err)

	assert.Empty(t, cfg.TrustedProxies)
}

func TestLoadTrustedProxies(t *testing.T) {
	setS3(t)
	t.Setenv(envEncryptionKeys, testEncryptionKeys)
	t.Setenv(envPublicURL, testPublicURL)
	cases := []struct {
		value string
		want  []string
	}{
		{"", nil},
		{" \t ", nil},
		{"127.0.0.1/32", []string{"127.0.0.1/32"}},
		{"::1/128", []string{"::1/128"}},
		{" 10.0.0.0/8, ::1/128 ", []string{"10.0.0.0/8", "::1/128"}},
	}
	for _, c := range cases {
		t.Run(c.value, func(t *testing.T) {
			t.Setenv(envHTTPAddr, "")
			t.Setenv(envSetupToken, "")
			t.Setenv(envTrustedProxies, c.value)

			cfg, err := Load()
			require.NoError(t, err)

			assert.Equal(t, c.want, cfg.TrustedProxies)
		})
	}
}

func TestLoadBadTrustedProxies(t *testing.T) {
	setS3(t)
	t.Setenv(envEncryptionKeys, testEncryptionKeys)
	t.Setenv(envPublicURL, testPublicURL)
	for _, value := range []string{"localhost", "127.0.0.1", "10.0.0.0/33", "::1/129", "10.0.0.0/8,", ",10.0.0.0/8", "10.0.0.0/8, ,::1/128"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(envHTTPAddr, "")
			t.Setenv(envSetupToken, "")
			t.Setenv(envTrustedProxies, value)

			_, err := Load()

			require.ErrorContains(t, err, envTrustedProxies)
		})
	}
}

func TestLoadSetupTokenUnset(t *testing.T) {
	setS3(t)
	t.Setenv(envEncryptionKeys, testEncryptionKeys)
	t.Setenv(envPublicURL, testPublicURL)
	t.Setenv(envHTTPAddr, "")
	t.Setenv(envSetupToken, "")
	t.Setenv(envTrustedProxies, "")
	require.NoError(t, os.Unsetenv(envSetupToken))

	cfg, err := Load()
	require.NoError(t, err)

	assert.Empty(t, cfg.SetupToken)
}

func TestLoadSetupToken(t *testing.T) {
	setS3(t)
	t.Setenv(envEncryptionKeys, testEncryptionKeys)
	t.Setenv(envPublicURL, testPublicURL)
	cases := []struct {
		name  string
		token string
	}{
		{"empty", ""},
		{"minimum", strings.Repeat("s", 32)},
		{"longer", strings.Repeat("s", 64)},
		{"Unicode", strings.Repeat("\u00e9", 32)},
		{"preserve spaces", " " + strings.Repeat("s", 30) + " "},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(envHTTPAddr, "")
			t.Setenv(envSetupToken, c.token)
			t.Setenv(envTrustedProxies, "")

			cfg, err := Load()
			require.NoError(t, err)

			assert.Equal(t, c.token, cfg.SetupToken)
		})
	}
}

func TestLoadShortSetupToken(t *testing.T) {
	setS3(t)
	t.Setenv(envEncryptionKeys, testEncryptionKeys)
	t.Setenv(envPublicURL, testPublicURL)
	cases := []struct {
		name  string
		token string
	}{
		{"short", "short-test-setup-token"},
		{"below minimum", strings.Repeat("s", 31)},
		{"Unicode below minimum", strings.Repeat("\u00e9", 31)},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(envHTTPAddr, "")
			t.Setenv(envSetupToken, c.token)
			t.Setenv(envTrustedProxies, "")

			cfg, err := Load()

			require.EqualError(t, err, "INBOK_SETUP_TOKEN must be at least 32 characters")
			assert.Empty(t, cfg.SetupToken)
			assert.NotContains(t, err.Error(), c.token)
		})
	}
}

func TestDatabaseURL(t *testing.T) {
	t.Setenv(envDatabaseURL, "postgres://u:p@db:5432/inbok")

	url, err := DatabaseURL()
	require.NoError(t, err)

	assert.Equal(t, "postgres://u:p@db:5432/inbok", url)
}

func TestDatabaseURLRequired(t *testing.T) {
	t.Setenv(envDatabaseURL, "")

	_, err := DatabaseURL()

	require.ErrorIs(t, err, errNoDatabaseURL)
}

func TestLoadEncryptionKeys(t *testing.T) {
	setS3(t)
	for _, value := range []string{"", "invalid-private-key", "1:bad-base64"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(envHTTPAddr, "")
			t.Setenv(envSetupToken, "")
			t.Setenv(envTrustedProxies, "")
			t.Setenv(envPublicURL, testPublicURL)
			t.Setenv(envEncryptionKeys, value)
			cfg, err := Load()
			require.ErrorContains(t, err, envEncryptionKeys)
			assert.Nil(t, cfg.Encryption)
			if value != "" {
				assert.NotContains(t, err.Error(), value)
			}
		})
	}
	t.Run("valid", func(t *testing.T) {
		t.Setenv(envHTTPAddr, "")
		t.Setenv(envSetupToken, "")
		t.Setenv(envTrustedProxies, "")
		t.Setenv(envEncryptionKeys, testEncryptionKeys)
		t.Setenv(envPublicURL, testPublicURL)
		cfg, err := Load()
		require.NoError(t, err)
		require.NotNil(t, cfg.Encryption)
		id, _, err := cfg.Encryption.Seal([]byte("secret"), "totp:1")
		require.NoError(t, err)
		assert.Equal(t, "1", id)
	})
}

func TestLoadPublicURL(t *testing.T) {
	setS3(t)
	t.Setenv(envEncryptionKeys, testEncryptionKeys)
	t.Setenv(envHTTPAddr, "")
	t.Setenv(envSetupToken, "")
	t.Setenv(envTrustedProxies, "")
	cases := []struct {
		value string
		want  string
	}{
		{"https://mail.example.com", "https://mail.example.com"},
		{"https://mail.example.com/", "https://mail.example.com"},
		{" https://mail.example.com ", "https://mail.example.com"},
		{"HTTPS://Mail.Example.com:8443", "https://mail.example.com:8443"},
		{"https://mäil.example.com", "https://xn--mil-qla.example.com"},
		{"https://mail.example.com:0443", "https://mail.example.com:443"},
		{"https://192.0.2.10", "https://192.0.2.10"},
		{"http://localhost:5173", "http://localhost:5173"},
		{"http://127.1.2.3", "http://127.1.2.3"},
		{"http://[::1]:5173/", "http://[::1]:5173"},
	}
	for _, c := range cases {
		t.Run(c.value, func(t *testing.T) {
			t.Setenv(envPublicURL, c.value)

			cfg, err := Load()
			require.NoError(t, err)

			require.NotNil(t, cfg.PublicURL)
			assert.Equal(t, c.want, cfg.PublicURL.String())
		})
	}
}

func TestLoadBadPublicURL(t *testing.T) {
	setS3(t)
	t.Setenv(envEncryptionKeys, testEncryptionKeys)
	t.Setenv(envHTTPAddr, "")
	t.Setenv(envSetupToken, "")
	t.Setenv(envTrustedProxies, "")
	cases := []struct {
		value string
		want  string
	}{
		{"", "INBOK_PUBLIC_URL is required"},
		{"  ", "INBOK_PUBLIC_URL is required"},

		// https, or http only for a loopback host.
		{"mail.example.com", "INBOK_PUBLIC_URL must use https"},
		{"ftp://mail.example.com", "INBOK_PUBLIC_URL must use https"},
		{"http://mail.example.com", "INBOK_PUBLIC_URL must use https"},
		{"http://localhost.example.com", "INBOK_PUBLIC_URL must use https"},
		{"http://192.0.2.10", "INBOK_PUBLIC_URL must use https"},
		{"http://0.0.0.0:5173", "INBOK_PUBLIC_URL must use https"},

		// The origin alone.
		{"https:mail.example.com", "INBOK_PUBLIC_URL must be"},
		{"https://", "INBOK_PUBLIC_URL must be"},
		{"https://mail.example.com/inbok", "INBOK_PUBLIC_URL must be"},
		{"https://mail.example.com//", "INBOK_PUBLIC_URL must be"},
		{"https://mail.example.com/?", "INBOK_PUBLIC_URL must be"},
		{"https://mail.example.com?a=1", "INBOK_PUBLIC_URL must be"},
		{"https://mail.example.com#", "INBOK_PUBLIC_URL must be"},
		{"https://mail.example.com#top", "INBOK_PUBLIC_URL must be"},
		{"https://user:secret@mail.example.com", "INBOK_PUBLIC_URL must be"},

		// A host name, checked by idna.
		{`https://mail.example.com"<b>`, "INBOK_PUBLIC_URL must be"},
		{"https://mail_box.example.com", "INBOK_PUBLIC_URL must be"},
		{"https://mail.example.com.", "INBOK_PUBLIC_URL must be"},

		// An IP address that names one host.
		{"https://0.0.0.0", "INBOK_PUBLIC_URL must be"},
		{"https://[::]", "INBOK_PUBLIC_URL must be"},
		{"https://[fe80::1%25en0]", "INBOK_PUBLIC_URL must be"},

		// A port from 1 to 65535.
		{"https://mail.example.com:", "INBOK_PUBLIC_URL must be"},
		{"https://mail.example.com:0", "INBOK_PUBLIC_URL must be"},
		{"https://mail.example.com:65536", "INBOK_PUBLIC_URL must be"},
	}
	for _, c := range cases {
		t.Run(c.value, func(t *testing.T) {
			t.Setenv(envPublicURL, c.value)

			cfg, err := Load()
			require.ErrorContains(t, err, c.want)

			assert.Nil(t, cfg.PublicURL)
			assert.NotContains(t, err.Error(), "secret")
		})
	}
}

// setGoogle sets the three Google settings for the test, and the
// settings [Load] requires.
func setGoogle(t *testing.T, id, secret, base string) {
	t.Helper()
	t.Setenv(envEncryptionKeys, testEncryptionKeys)
	t.Setenv(envPublicURL, testPublicURL)
	t.Setenv(envHTTPAddr, "")
	t.Setenv(envSetupToken, "")
	t.Setenv(envTrustedProxies, "")
	t.Setenv(envGoogleClientID, id)
	t.Setenv(envGoogleClientSecret, secret)
	t.Setenv(envGoogleBaseURL, base)
	setS3(t)
}

func TestLoadGoogleUnset(t *testing.T) {
	setGoogle(t, "", " ", "")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Nil(t, cfg.Google)
}

func TestLoadGoogle(t *testing.T) {
	setGoogle(t, " inbok.apps.googleusercontent.com\n", "client-secret\n", "")

	cfg, err := Load()
	require.NoError(t, err)

	require.NotNil(t, cfg.Google)
	assert.Equal(t, "inbok.apps.googleusercontent.com", cfg.Google.ClientID)
	assert.Equal(t, "client-secret", cfg.Google.ClientSecret)
	assert.Nil(t, cfg.Google.BaseURL)
}

func TestLoadGoogleBaseURL(t *testing.T) {
	cases := []struct {
		value string
		want  string
	}{
		{"http://localhost:4000", "http://localhost:4000"},
		{"http://LocalHost:4000/", "http://localhost:4000"},
		{"https://127.0.0.1:4443", "https://127.0.0.1:4443"},
		{"http://127.9.9.9", "http://127.9.9.9"},
		{"http://[::1]:4000", "http://[::1]:4000"},
	}
	for _, c := range cases {
		t.Run(c.value, func(t *testing.T) {
			setGoogle(t, "id", "secret", c.value)

			cfg, err := Load()
			require.NoError(t, err)

			require.NotNil(t, cfg.Google)
			require.NotNil(t, cfg.Google.BaseURL)
			assert.Equal(t, c.want, cfg.Google.BaseURL.String())
		})
	}
}

func TestLoadBadGoogle(t *testing.T) {
	cases := []struct {
		name             string
		id, secret, base string
		want             string
	}{
		{"id alone", "client-id-x", "", "", "must be set together"},
		{"secret alone", "", "client-secret-x", "", "must be set together"},
		{"base alone", "", "", "http://localhost:4000", "INBOK_GOOGLE_BASE_URL needs"},

		// Another machine, or not http.
		{"remote https", "client-id-x", "client-secret-x", "https://oauth2.googleapis.com", "must be an http or https address on this machine"},
		{"remote ip", "client-id-x", "client-secret-x", "http://192.0.2.10:4000", "must be an http or https address on this machine"},
		{"lookalike", "client-id-x", "client-secret-x", "http://localhost.example.com", "must be an http or https address on this machine"},
		{"unspecified", "client-id-x", "client-secret-x", "http://0.0.0.0:4000", "must be an http or https address on this machine"},
		{"no scheme", "client-id-x", "client-secret-x", "localhost:4000", "must be an http or https address on this machine"},
		{"ftp", "client-id-x", "client-secret-x", "ftp://localhost", "must be an http or https address on this machine"},

		// The origin alone.
		{"path", "client-id-x", "client-secret-x", "http://localhost:4000/google", "INBOK_GOOGLE_BASE_URL must be only"},
		{"query", "client-id-x", "client-secret-x", "http://localhost:4000?x=1", "INBOK_GOOGLE_BASE_URL must be only"},
		{"user info", "client-id-x", "client-secret-x", "http://user:secret@localhost:4000", "INBOK_GOOGLE_BASE_URL must be only"},
		{"port", "client-id-x", "client-secret-x", "http://localhost:0", "INBOK_GOOGLE_BASE_URL must be only"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setGoogle(t, c.id, c.secret, c.base)

			cfg, err := Load()
			require.ErrorContains(t, err, c.want)

			assert.Nil(t, cfg.Google)
			// Errors name settings, never their values.
			for _, value := range []string{c.id, c.secret, c.base} {
				if value != "" {
					assert.NotContains(t, err.Error(), value)
				}
			}
		})
	}
}

// Without a setting the worker is "worker", so a replaced worker resumes
// the work its predecessor left unfinished.
func TestWorkerID(t *testing.T) {
	cases := map[string]string{"": "worker", "worker-2": "worker-2", "a.b_C9": "a.b_C9"}
	for value, want := range cases {
		t.Run(value, func(t *testing.T) {
			t.Setenv(envWorkerID, value)

			id, err := WorkerID()
			require.NoError(t, err)

			assert.Equal(t, want, id)
		})
	}
}

// An ID that is too long or has other characters stops the worker and names
// the setting.
func TestWorkerIDBad(t *testing.T) {
	for _, value := range []string{" ", "worker 2", "w/1", "w\n", strings.Repeat("w", 65)} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(envWorkerID, value)

			_, err := WorkerID()

			require.ErrorContains(t, err, envWorkerID)
		})
	}
}

func TestSearchIndex(t *testing.T) {
	cases := map[string]string{
		"http://127.0.0.1:7700":      "http://127.0.0.1:7700",
		" http://meilisearch:7700/ ": "http://meilisearch:7700",
		"https://search.example.com": "https://search.example.com",
	}
	for value, want := range cases {
		t.Run(value, func(t *testing.T) {
			t.Setenv(envSearchURL, value)
			t.Setenv(envSearchKey, "a-test-master-key")

			address, key, err := SearchIndex()
			require.NoError(t, err)

			assert.Equal(t, want, address)
			assert.Equal(t, "a-test-master-key", key)
		})
	}
}

func TestLoadS3(t *testing.T) {
	setS3(t)
	t.Setenv(envEncryptionKeys, testEncryptionKeys)
	t.Setenv(envPublicURL, testPublicURL)

	cfg, err := Load()
	require.NoError(t, err)

	require.NotNil(t, cfg.S3.Endpoint)
	assert.Equal(t, testS3Endpoint, cfg.S3.Endpoint.String())
	assert.Equal(t, testS3AccessKeyID, cfg.S3.AccessKeyID)
	assert.Equal(t, testS3SecretAccessKey, cfg.S3.SecretAccessKey)
}

// The worker reads the settings without serve's others, e.g. the setup
// token.
func TestS3SettingsEndpoint(t *testing.T) {
	setS3(t)
	cases := map[string]string{
		"http://seaweedfs:8333":      "http://seaweedfs:8333",
		"http://127.0.0.1:8333/":     "http://127.0.0.1:8333",
		" https://s3.example.com ":   "https://s3.example.com",
		"HTTP://SeaweedFS:8333":      "http://seaweedfs:8333",
		"http://[::1]:8333":          "http://[::1]:8333",
		"https://s3.example.com:443": "https://s3.example.com:443",
	}
	for value, want := range cases {
		t.Run(value, func(t *testing.T) {
			t.Setenv(envS3Endpoint, value)

			store, err := S3Settings()
			require.NoError(t, err)

			assert.Equal(t, want, store.Endpoint.String())
		})
	}
}

// Neither setting has a default, and an error never shows the key.
func TestSearchIndexBad(t *testing.T) {
	const key = "a-test-master-key"
	cases := map[string]struct{ address, key, want string }{
		"no address":   {"", key, envSearchURL},
		"no scheme":    {"meilisearch:7700", key, envSearchURL},
		"other scheme": {"ftp://meilisearch:7700", key, envSearchURL},
		"no host":      {"http://", key, envSearchURL},
		"path":         {"http://meilisearch:7700/search", key, envSearchURL},
		"query":        {"http://meilisearch:7700?a=1", key, envSearchURL},
		"user":         {"http://" + key + "@meilisearch:7700", key, envSearchURL},
		"no key":       {"http://meilisearch:7700", "", envSearchKey},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv(envSearchURL, c.address)
			t.Setenv(envSearchKey, c.key)

			_, _, err := SearchIndex()

			require.ErrorContains(t, err, c.want)
			assert.NotContains(t, err.Error(), key)
		})
	}
}

func TestS3SettingsBadEndpoint(t *testing.T) {
	setS3(t)
	cases := []struct {
		value string
		want  string
	}{
		{"", "INBOK_S3_ENDPOINT is required"},
		{"  ", "INBOK_S3_ENDPOINT is required"},
		{"seaweedfs:8333", "INBOK_S3_ENDPOINT must be"},
		{"ftp://seaweedfs:8333", "INBOK_S3_ENDPOINT must be"},
		{"http://", "INBOK_S3_ENDPOINT must be"},
		{"http://seaweedfs:8333/inbok", "INBOK_S3_ENDPOINT must be"},
		{"http://seaweedfs:8333?a=1", "INBOK_S3_ENDPOINT must be"},
		{"http://seaweedfs:8333#top", "INBOK_S3_ENDPOINT must be"},
		{"http://user:secret@seaweedfs:8333", "INBOK_S3_ENDPOINT must be"},
		{"http://seaweedfs:port", "INBOK_S3_ENDPOINT must be"},
		{"http://:8333", "INBOK_S3_ENDPOINT must be"},
		{"http://seaweedfs:", "INBOK_S3_ENDPOINT must be"},
		{"http://seaweedfs:0", "INBOK_S3_ENDPOINT must be"},
		{"http://seaweedfs:65536", "INBOK_S3_ENDPOINT must be"},
	}
	for _, c := range cases {
		t.Run(c.value, func(t *testing.T) {
			t.Setenv(envS3Endpoint, c.value)

			_, err := S3Settings()

			require.ErrorContains(t, err, c.want)
			assert.NotContains(t, err.Error(), "secret")
		})
	}
}

// Keys are taken as they are: a space may be part of a generated secret.
func TestS3SettingsKeys(t *testing.T) {
	setS3(t)
	t.Setenv(envS3SecretAccessKey, " spaced secret ")

	store, err := S3Settings()
	require.NoError(t, err)

	assert.Equal(t, " spaced secret ", store.SecretAccessKey)
}

func TestS3SettingsKeysRequired(t *testing.T) {
	for _, name := range []string{envS3AccessKeyID, envS3SecretAccessKey} {
		t.Run(name, func(t *testing.T) {
			setS3(t)
			t.Setenv(name, "")

			_, err := S3Settings()

			require.EqualError(t, err, name+" is required")
		})
	}
}
