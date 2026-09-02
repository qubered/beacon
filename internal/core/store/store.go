package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the only thing in the codebase that speaks SQL.
//
// Everything else takes a repository interface, which is what makes the
// eventual time-series extension a drop-in rather than a migration (D22). It
// is also what keeps site scoping honest: every method here takes a site.ID as
// an explicit parameter rather than reading one from a context, so forgetting
// to scope a query is a compile error rather than a data leak (D30).
type Store struct {
	pool *pgxpool.Pool
}

// Open connects and verifies the connection. It does not run migrations —
// `beaconctl migrate` does that, deliberately separately, so a process
// starting up cannot silently reshape the database it found.
func Open(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parsing database URL: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connecting to the database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("database is unreachable: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

// Pool exposes the connection pool for the migration runner, which needs to
// execute arbitrary DDL that no repository method should offer.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }
