package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// worker names a missing or bad setting before opening a database.
func TestWorkerSettings(t *testing.T) {
	const (
		url  = "postgres://u:p@127.0.0.1:1/amplop"
		keys = "1:AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE="
		site = "https://mail.example.com"
		// Nothing listens on port 1, so a worker that went on would fail
		// there instead.
		search = "http://127.0.0.1:1"
		store  = "http://127.0.0.1:1"
	)
	cases := map[string]struct{ url, id, site, keys, search, store, want string }{
		"no database":     {"", "", site, keys, search, store, "AMPLOP_DATABASE_URL"},
		"bad worker ID":   {url, "w 1", site, keys, search, store, "AMPLOP_WORKER_ID"},
		"no public URL":   {url, "", "", keys, search, store, "AMPLOP_PUBLIC_URL"},
		"no keys":         {url, "", site, "", search, store, "AMPLOP_ENCRYPTION_KEYS"},
		"no search URL":   {url, "", site, keys, "", store, "AMPLOP_MEILISEARCH_URL"},
		"no object store": {url, "", site, keys, search, "", "AMPLOP_S3_ENDPOINT"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("AMPLOP_HTTP_ADDR", "")
			t.Setenv("AMPLOP_DATABASE_URL", c.url)
			t.Setenv("AMPLOP_WORKER_ID", c.id)
			t.Setenv("AMPLOP_PUBLIC_URL", c.site)
			t.Setenv("AMPLOP_ENCRYPTION_KEYS", c.keys)
			t.Setenv("AMPLOP_MEILISEARCH_URL", c.search)
			t.Setenv("AMPLOP_MEILISEARCH_KEY", "a-test-master-key")
			t.Setenv("AMPLOP_S3_ENDPOINT", c.store)
			t.Setenv("AMPLOP_S3_ACCESS_KEY_ID", "test-key-id")
			t.Setenv("AMPLOP_S3_SECRET_ACCESS_KEY", "test-secret-access-key")

			err := run(context.Background(), []string{"worker"}, discard)

			require.ErrorContains(t, err, c.want)
		})
	}
}

// A search index that does not answer stops the worker before it opens
// the database.
func TestWorkerSearchDown(t *testing.T) {
	t.Setenv("AMPLOP_HTTP_ADDR", "")
	t.Setenv("AMPLOP_DATABASE_URL", "postgres://u:p@127.0.0.1:1/amplop")
	t.Setenv("AMPLOP_WORKER_ID", "")
	t.Setenv("AMPLOP_PUBLIC_URL", "https://mail.example.com")
	t.Setenv("AMPLOP_ENCRYPTION_KEYS", "1:AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=")
	t.Setenv("AMPLOP_MEILISEARCH_URL", "http://127.0.0.1:1")
	t.Setenv("AMPLOP_MEILISEARCH_KEY", "a-test-master-key")
	t.Setenv("AMPLOP_S3_ENDPOINT", "http://127.0.0.1:1")
	t.Setenv("AMPLOP_S3_ACCESS_KEY_ID", "test-key-id")
	t.Setenv("AMPLOP_S3_SECRET_ACCESS_KEY", "test-secret-access-key")

	err := run(context.Background(), []string{"worker"}, discard)

	require.ErrorContains(t, err, "list search indexes")
}
