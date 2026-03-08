package entitystore

import "os"

// Config holds the configuration for the entity store service.
type Config struct {
	DatabaseURL string
}

// ConfigFromEnv creates a Config from environment variables.
func ConfigFromEnv() Config {
	return Config{
		DatabaseURL: envOr("DATABASE_URL", "postgres://postgres:postgres@localhost:5433/entitystore?sslmode=disable"),
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
