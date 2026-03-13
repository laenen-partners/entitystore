package entitystore

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/laenen-partners/entitystore/store"
)

// Option configures an EntityStore.
type Option func(*options)

type options struct {
	store *store.Store
}

// WithPgStore configures the EntityStore to use a PostgreSQL backend with
// the given connection pool. The caller retains ownership of the pool and
// is responsible for closing it.
func WithPgStore(pool *pgxpool.Pool) Option {
	return func(o *options) {
		o.store = store.NewFromPool(pool)
	}
}
