package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// worker names a missing or bad setting before opening a database.
func TestWorkerSettings(t *testing.T) {
	cases := map[string]struct{ url, id, want string }{
		"no database":   {"", "", "INBOK_DATABASE_URL"},
		"bad worker ID": {"postgres://u:p@127.0.0.1:1/inbok", "w 1", "INBOK_WORKER_ID"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("INBOK_HTTP_ADDR", "")
			t.Setenv("INBOK_DATABASE_URL", c.url)
			t.Setenv("INBOK_WORKER_ID", c.id)

			err := run(context.Background(), []string{"worker"}, discard)

			require.ErrorContains(t, err, c.want)
		})
	}
}
