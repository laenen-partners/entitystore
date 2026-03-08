package entitystore

import (
	"context"
	"fmt"
	"net/http"

	"github.com/laenen-partners/entitystore/gen/entitystore/v1/entitystorev1connect"
	entitystore "github.com/laenen-partners/entitystore/store"
)

// New creates an http.Handler serving the EntityStoreService.
// The caller is responsible for running the HTTP server.
func New(cfg Config) (http.Handler, *entitystore.Store, error) {
	ctx := context.Background()

	s, err := entitystore.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, nil, fmt.Errorf("entitystore: open store: %w", err)
	}

	path, rpcHandler := entitystorev1connect.NewEntityStoreServiceHandler(
		&Handler{store: s},
	)

	mux := http.NewServeMux()
	mux.Handle(path, rpcHandler)

	return mux, s, nil
}
