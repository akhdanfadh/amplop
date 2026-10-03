package postgres

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"
)

// Without connect_timeout, pgx waits forever for a server that accepts the
// connection but never answers; Open must give up on its own. A URL value
// still wins.
func TestPoolConfigTimeout(t *testing.T) {
	cases := []struct {
		url  string
		want time.Duration
	}{
		{"postgres://u:p@db/inbok", defaultConnectTimeout},
		{"postgres://u:p@db/inbok?connect_timeout=2", 2 * time.Second},
	}

	for _, c := range cases {
		cfg, err := poolConfig(c.url)
		if err != nil {
			t.Fatal(err)
		}
		if got := cfg.ConnConfig.ConnectTimeout; got != c.want {
			t.Errorf("%s: ConnectTimeout = %v, want %v", c.url, got, c.want)
		}
	}
}

// A host that accepts connections but never answers, e.g. behind a broken
// firewall, must not hang startup or migrate: both give up on their own,
// before ctx ends.
func TestConnectTimeout(t *testing.T) {
	url := silentServer(t)
	cases := []struct {
		name    string
		connect func(ctx context.Context) error
	}{
		{"Open", func(ctx context.Context) error {
			db, err := Open(ctx, url)
			if err == nil {
				db.Close()
			}
			return err
		}},
		{"Migrate", func(ctx context.Context) error { return Migrate(ctx, url, discard) }},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(t.Context(), 2*defaultConnectTimeout)
			defer cancel()

			start := time.Now()
			if err := c.connect(ctx); err == nil || ctx.Err() != nil {
				t.Fatalf("err = %v after %v; want a connect timeout before ctx ends", err, time.Since(start))
			}
		})
	}
}

// silentServer accepts TCP connections and never answers. It returns a URL
// without connect_timeout.
func silentServer(t *testing.T) string {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var conns []net.Conn
	t.Cleanup(func() {
		listener.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, conn := range conns {
			conn.Close()
		}
	})

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
		}
	}()

	return "postgres://inbok:secret@" + listener.Addr().String() + "/inbok?sslmode=disable"
}
