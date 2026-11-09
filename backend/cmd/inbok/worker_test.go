package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// worker names a missing or bad setting before opening a database.
func TestWorkerSettings(t *testing.T) {
	const (
		url  = "postgres://u:p@127.0.0.1:1/inbok"
		keys = "1:AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE="
		site = "https://mail.example.com"
		// Nothing listens on port 1, so a worker that went on would fail
		// there instead.
		search = "http://127.0.0.1:1"
	)
	cases := map[string]struct{ url, id, site, keys, search, want string }{
		"no database":   {"", "", site, keys, search, "INBOK_DATABASE_URL"},
		"bad worker ID": {url, "w 1", site, keys, search, "INBOK_WORKER_ID"},
		"no public URL": {url, "", "", keys, search, "INBOK_PUBLIC_URL"},
		"no keys":       {url, "", site, "", search, "INBOK_ENCRYPTION_KEYS"},
		"no search URL": {url, "", site, keys, "", "INBOK_MEILISEARCH_URL"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("INBOK_HTTP_ADDR", "")
			t.Setenv("INBOK_DATABASE_URL", c.url)
			t.Setenv("INBOK_WORKER_ID", c.id)
			t.Setenv("INBOK_PUBLIC_URL", c.site)
			t.Setenv("INBOK_ENCRYPTION_KEYS", c.keys)
			t.Setenv("INBOK_MEILISEARCH_URL", c.search)
			t.Setenv("INBOK_MEILISEARCH_KEY", "a-test-master-key")

			err := run(context.Background(), []string{"worker"}, discard)

			require.ErrorContains(t, err, c.want)
		})
	}
}

// A search index that does not answer stops the worker before it opens
// the database.
func TestWorkerSearchDown(t *testing.T) {
	t.Setenv("INBOK_HTTP_ADDR", "")
	t.Setenv("INBOK_DATABASE_URL", "postgres://u:p@127.0.0.1:1/inbok")
	t.Setenv("INBOK_WORKER_ID", "")
	t.Setenv("INBOK_PUBLIC_URL", "https://mail.example.com")
	t.Setenv("INBOK_ENCRYPTION_KEYS", "1:AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=")
	t.Setenv("INBOK_MEILISEARCH_URL", "http://127.0.0.1:1")
	t.Setenv("INBOK_MEILISEARCH_KEY", "a-test-master-key")

	err := run(context.Background(), []string{"worker"}, discard)

	require.ErrorContains(t, err, "list search indexes")
}
