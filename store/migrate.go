package store

import (
	"embed"
	"fmt"
	"io"
	"net/url"

	"github.com/amacneil/dbmate/v2/pkg/dbmate"
	_ "github.com/amacneil/dbmate/v2/pkg/driver/postgres"
)

//go:embed db/migrations/*.sql
var migrationsFS embed.FS

const migrationsTableName = "entitystore_migrations"

// Migrate applies all pending migrations using dbmate. It tracks applied
// migrations in the "entitystore_migrations" table.
func Migrate(connString string) error {
	u, err := url.Parse(connString)
	if err != nil {
		return fmt.Errorf("migrate: parse url: %w", err)
	}
	db := dbmate.New(u)
	db.FS = migrationsFS
	db.MigrationsDir = []string{"db/migrations"}
	db.MigrationsTableName = migrationsTableName
	db.AutoDumpSchema = false
	db.Log = io.Discard
	if err := db.Migrate(); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}
