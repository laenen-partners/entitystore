package entitystore

import (
	"context"
	"crypto/subtle"
	"errors"
	"strings"

	"connectrpc.com/connect"
)

// NewAuthInterceptor returns a Connect unary interceptor that validates
// API keys from the Authorization header using the Bearer scheme and
// propagates caller identity (X-User-ID, X-Service-ID) into the context.
func NewAuthInterceptor(apiKeys []string) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			auth := req.Header().Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") {
				return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("missing or invalid authorization header"))
			}
			key := strings.TrimPrefix(auth, "Bearer ")

			valid := false
			for _, allowed := range apiKeys {
				if subtle.ConstantTimeCompare([]byte(key), []byte(allowed)) == 1 {
					valid = true
					break
				}
			}
			if !valid {
				return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid API key"))
			}

			caller := Caller{
				ServiceID: req.Header().Get("X-Service-ID"),
				UserID:    req.Header().Get("X-User-ID"),
			}
			ctx = withCaller(ctx, caller)

			return next(ctx, req)
		}
	}
}
