package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DB stores inbok's data in PostgreSQL. Each feature adds its methods.
type DB struct {
	pool *pgxpool.Pool
}

// defaultConnectTimeout bounds each new connection unless the URL sets
// connect_timeout. pgx has no default, so a silent host would hang it.
const defaultConnectTimeout = 5 * time.Second

// Open connects to url and checks that the database answers, so a wrong
// URL fails at startup instead of on the first request.
func Open(ctx context.Context, url string) (*DB, error) {
	cfg, err := poolConfig(url)
	if err != nil {
		return nil, err
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}

	return &DB{pool: pool}, nil
}

// Close waits for borrowed connections to return, then closes them all.
func (db *DB) Close() {
	db.pool.Close()
}

// poolConfig parses url into the pool's settings.
func poolConfig(url string) (*pgxpool.Config, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}

	if cfg.ConnConfig.ConnectTimeout == 0 {
		cfg.ConnConfig.ConnectTimeout = defaultConnectTimeout
	}
	return cfg, nil
}
