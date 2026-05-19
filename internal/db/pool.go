package db

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Pool wraps a pgxpool.Pool so callers never touch *pgxpool directly.
type Pool struct {
	*pgxpool.Pool
}

// NewPool reads DATABASE_URL and returns a connected pool.
func NewPool() (*Pool, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return nil, fmt.Errorf("DATABASE_URL not set")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	cfg.MaxConns = 10
	cfg.MinConns = 2
	cfg.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(nil, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	// quick ping
	ctx, cancel := contextWithTimeout(3 * time.Second)
	defer cancel()
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	log.Println("db pool ready")
	return &Pool{pool}, nil
}
