package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/ananddub/ndiscord_backend/internal/shared/logger"
)

// HTTPLoggingMiddleware emits a structured log line per request. Runs on
// the REST gateway only; the gRPC LoggingInterceptor still covers native
// gRPC calls. URL-level visibility here complements method-level gRPC logs.
func HTTPLoggingMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w, status: 200}
			next.ServeHTTP(sw, r)
			logger.Log.Info().
				Str("method", r.Method).
				Str("path", r.URL.Path).
				Int("status", sw.status).
				Dur("duration", time.Since(start)).
				Str("user_id", UserIDFromContext(r.Context())).
				Str("remote", httpPeerIP(r)).
				Msg("http request")
		})
	}
}

// httpPeerIP honors reverse-proxy headers when present, else falls back to
// RemoteAddr. X-Forwarded-For may be a comma-separated chain — take the
// left-most entry (the original client).
func httpPeerIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if comma := strings.IndexByte(xff, ','); comma > 0 {
			return strings.TrimSpace(xff[:comma])
		}
		return strings.TrimSpace(xff)
	}
	if xr := r.Header.Get("X-Real-IP"); xr != "" {
		return strings.TrimSpace(xr)
	}
	addr := r.RemoteAddr
	if idx := strings.LastIndex(addr, ":"); idx != -1 {
		return addr[:idx]
	}
	return addr
}

// statusWriter captures response status for logging and passes Flush
// through so SSE streams still work while wrapped.
type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusWriter) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
		s.ResponseWriter.WriteHeader(code)
	}
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		s.wroteHeader = true
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
