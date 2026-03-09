package entitystore

import (
	"context"
	"fmt"
	"net/http"

	"connectrpc.com/connect"

	"github.com/laenen-partners/entitystore/gen/entitystore/v1/entitystorev1connect"
	entitystore "github.com/laenen-partners/entitystore/store"
)

// New creates an http.Handler serving the EntityStoreService with auth,
// middleware stack, and health endpoints. The caller is responsible for
// running the HTTP server.
func New(cfg Config) (http.Handler, *entitystore.Store, error) {
	ctx := context.Background()

	s, err := entitystore.New(ctx, cfg.DatabaseURL,
		entitystore.WithPoolConfig(cfg.DBMaxConns, cfg.DBMinConns, cfg.DBConnIdleTime),
		entitystore.WithAutoMigrate(),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("entitystore: open store: %w", err)
	}

	mux := http.NewServeMux()

	// Health check endpoints (unauthenticated).
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	})

	// Mount Connect-RPC handler with optional auth interceptor.
	var opts []connect.HandlerOption
	if len(cfg.APIKeys) > 0 {
		opts = append(opts, connect.WithInterceptors(NewAuthInterceptor(cfg.APIKeys)))
	}
	path, rpcHandler := entitystorev1connect.NewEntityStoreServiceHandler(
		&Handler{store: s}, opts...,
	)
	mux.Handle(path, rpcHandler)

	// Apply middleware stack: rate limiting (outermost) → CORS → logging → security headers.
	var handler http.Handler = mux
	handler = SecurityHeaders(handler)
	handler = RequestLogging(handler)
	if len(cfg.CORSOrigins) > 0 {
		handler = CORS(cfg.CORSOrigins)(handler)
	}
	if cfg.RateLimit > 0 {
		handler = RateLimit(cfg.RateLimit, cfg.RateBurst)(handler)
	}

	return handler, s, nil
}
