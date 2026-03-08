package entitystore

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds the configuration for the entity store service.
type Config struct {
	Addr            string        // server listen address (default ":3002")
	DatabaseURL     string        // PostgreSQL connection string
	APIKeys         []string      // API keys for RPC authentication
	RateLimit       float64       // requests per second per IP (0 = disabled)
	RateBurst       int           // burst allowance per IP
	CORSOrigins     []string      // allowed CORS origins (empty = no CORS)
	DBMaxConns      int32         // max open database connections (default 10)
	DBMinConns      int32         // min idle database connections (default 2)
	DBConnIdleTime  time.Duration // max idle time before closing a connection (default 30m)
}

// ConfigFromEnv creates a Config from environment variables.
func ConfigFromEnv() Config {
	var apiKeys []string
	if keys := os.Getenv("API_KEYS"); keys != "" {
		for _, k := range strings.Split(keys, ",") {
			if trimmed := strings.TrimSpace(k); trimmed != "" {
				apiKeys = append(apiKeys, trimmed)
			}
		}
	}

	rateLimit := 10.0
	if v := os.Getenv("RATE_LIMIT"); v != "" {
		if parsed, err := strconv.ParseFloat(v, 64); err == nil {
			rateLimit = parsed
		}
	}

	rateBurst := 20
	if v := os.Getenv("RATE_BURST"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			rateBurst = parsed
		}
	}

	var corsOrigins []string
	if v := os.Getenv("CORS_ORIGINS"); v != "" {
		for _, o := range strings.Split(v, ",") {
			if trimmed := strings.TrimSpace(o); trimmed != "" {
				corsOrigins = append(corsOrigins, trimmed)
			}
		}
	}

	dbMaxConns := int32(10)
	if v := os.Getenv("DB_MAX_CONNS"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			dbMaxConns = int32(parsed)
		}
	}

	dbMinConns := int32(2)
	if v := os.Getenv("DB_MIN_CONNS"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed >= 0 {
			dbMinConns = int32(parsed)
		}
	}

	dbConnIdleTime := 30 * time.Minute
	if v := os.Getenv("DB_CONN_IDLE_TIME"); v != "" {
		if parsed, err := time.ParseDuration(v); err == nil {
			dbConnIdleTime = parsed
		}
	}

	return Config{
		Addr:           envOr("ADDR", ":3002"),
		DatabaseURL:    envOr("DATABASE_URL", "postgres://postgres:postgres@localhost:5433/entitystore?sslmode=disable"),
		APIKeys:        apiKeys,
		RateLimit:      rateLimit,
		RateBurst:      rateBurst,
		CORSOrigins:    corsOrigins,
		DBMaxConns:     dbMaxConns,
		DBMinConns:     dbMinConns,
		DBConnIdleTime: dbConnIdleTime,
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
