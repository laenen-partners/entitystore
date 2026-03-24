package entitystore

import (
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/laenen-partners/entitystore/store"
)

// Option configures an EntityStore.
type Option func(*options)

type options struct {
	pool     *pgxpool.Pool
	storeOps []store.Option
}

// WithPgStore configures the EntityStore to use a PostgreSQL backend with
// the given connection pool. The caller retains ownership of the pool and
// is responsible for closing it.
func WithPgStore(pool *pgxpool.Pool) Option {
	return func(o *options) {
		o.pool = pool
	}
}

// WithLogger sets a structured logger for the store.
func WithLogger(l *slog.Logger) Option {
	return func(o *options) {
		o.storeOps = append(o.storeOps, store.WithLogger(l))
	}
}
