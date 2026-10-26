package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadDefault(t *testing.T) {
	t.Setenv(envHTTPAddr, "")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "127.0.0.1:8080", cfg.HTTPAddr)
}

func TestLoadAddr(t *testing.T) {
	t.Setenv(envHTTPAddr, "0.0.0.0:9090")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "0.0.0.0:9090", cfg.HTTPAddr)
}

func TestLoadBadAddr(t *testing.T) {
	t.Setenv(envHTTPAddr, "localhost")

	_, err := Load()

	require.ErrorContains(t, err, envHTTPAddr)
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
