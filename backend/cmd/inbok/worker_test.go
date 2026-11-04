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
	)
	cases := map[string]struct{ url, id, site, keys, want string }{
		"no database":   {"", "", site, keys, "INBOK_DATABASE_URL"},
		"bad worker ID": {url, "w 1", site, keys, "INBOK_WORKER_ID"},
		"no public URL": {url, "", "", keys, "INBOK_PUBLIC_URL"},
		"no keys":       {url, "", site, "", "INBOK_ENCRYPTION_KEYS"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("INBOK_HTTP_ADDR", "")
			t.Setenv("INBOK_DATABASE_URL", c.url)
			t.Setenv("INBOK_WORKER_ID", c.id)
			t.Setenv("INBOK_PUBLIC_URL", c.site)
			t.Setenv("INBOK_ENCRYPTION_KEYS", c.keys)

			err := run(context.Background(), []string{"worker"}, discard)

			require.ErrorContains(t, err, c.want)
		})
	}
}
