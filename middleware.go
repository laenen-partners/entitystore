package entitystore

import (
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type responseWriter struct {
	http.ResponseWriter
	status  int
	size    int
	written bool
}

func (w *responseWriter) WriteHeader(code int) {
	if !w.written {
		w.status = code
		w.written = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *responseWriter) Write(b []byte) (int, error) {
	if !w.written {
		w.status = http.StatusOK
		w.written = true
	}
	n, err := w.ResponseWriter.Write(b)
	w.size += n
	return n, err
}

// RequestLogging returns middleware that logs each HTTP request.
func RequestLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)

		attrs := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.status,
			"size", rw.size,
			"duration", time.Since(start),
			"ip", clientIP(r),
		}
		if sid := r.Header.Get("X-Service-ID"); sid != "" {
			attrs = append(attrs, "service_id", sid)
		}
		if uid := r.Header.Get("X-User-ID"); uid != "" {
			attrs = append(attrs, "user_id", uid)
		}
		slog.Info("http request", attrs...)
	})
}

// SecurityHeaders returns middleware that sets security response headers.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "0")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

// RateLimit returns middleware that rate-limits requests by client IP
// using a token bucket algorithm.
func RateLimit(rps float64, burst int) func(http.Handler) http.Handler {
	limiter := newIPLimiter(rps, burst)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !limiter.allow(clientIP(r)) {
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// CORS returns middleware that handles Cross-Origin Resource Sharing for the given origins.
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowed[o] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" && allowed[origin] {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, HEAD")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-User-ID, X-Service-ID, Connect-Protocol-Version")
				w.Header().Set("Access-Control-Max-Age", "86400")
				w.Header().Set("Vary", "Origin")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if parts := strings.SplitN(xff, ",", 2); len(parts) > 0 {
			return strings.TrimSpace(parts[0])
		}
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type ipLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitor
	rate     float64
	burst    float64
}

type visitor struct {
	tokens float64
	last   time.Time
}

func newIPLimiter(rps float64, burst int) *ipLimiter {
	l := &ipLimiter{
		visitors: make(map[string]*visitor),
		rate:     rps,
		burst:    float64(burst),
	}
	go l.cleanup()
	return l
}

func (l *ipLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	v, exists := l.visitors[ip]
	if !exists {
		l.visitors[ip] = &visitor{tokens: l.burst - 1, last: time.Now()}
		return true
	}

	now := time.Now()
	elapsed := now.Sub(v.last).Seconds()
	v.tokens = min(l.burst, v.tokens+elapsed*l.rate)
	v.last = now

	if v.tokens >= 1 {
		v.tokens--
		return true
	}
	return false
}

func (l *ipLimiter) cleanup() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		l.mu.Lock()
		for ip, v := range l.visitors {
			if time.Since(v.last) > 10*time.Minute {
				delete(l.visitors, ip)
			}
		}
		l.mu.Unlock()
	}
}
