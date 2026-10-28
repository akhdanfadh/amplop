package config

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadDefault(t *testing.T) {
	t.Setenv(envHTTPAddr, "")
	t.Setenv(envSetupToken, "")
	t.Setenv(envTrustedProxies, "")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "127.0.0.1:8080", cfg.HTTPAddr)
	assert.Empty(t, cfg.TrustedProxies)
}

func TestLoadAddr(t *testing.T) {
	t.Setenv(envHTTPAddr, "0.0.0.0:9090")
	t.Setenv(envSetupToken, "")
	t.Setenv(envTrustedProxies, "")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "0.0.0.0:9090", cfg.HTTPAddr)
}

func TestLoadBadAddr(t *testing.T) {
	t.Setenv(envHTTPAddr, "localhost")
	t.Setenv(envSetupToken, "")
	t.Setenv(envTrustedProxies, "")

	_, err := Load()

	require.ErrorContains(t, err, envHTTPAddr)
}

func TestLoadTrustedProxiesUnset(t *testing.T) {
	t.Setenv(envHTTPAddr, "")
	t.Setenv(envSetupToken, "")
	t.Setenv(envTrustedProxies, "")
	require.NoError(t, os.Unsetenv(envTrustedProxies))

	cfg, err := Load()
	require.NoError(t, err)

	assert.Empty(t, cfg.TrustedProxies)
}

func TestLoadTrustedProxies(t *testing.T) {
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
	t.Setenv(envHTTPAddr, "")
	t.Setenv(envSetupToken, "")
	t.Setenv(envTrustedProxies, "")
	require.NoError(t, os.Unsetenv(envSetupToken))

	cfg, err := Load()
	require.NoError(t, err)

	assert.Empty(t, cfg.SetupToken)
}

func TestLoadSetupToken(t *testing.T) {
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
