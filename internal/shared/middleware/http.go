package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"

	"github.com/ananddub/ndiscord_backend/internal/shared/logger"
)

// HTTP middleware mirroring the gRPC interceptors so REST traffic picks up
// the same auth, rate-limit, and logging behaviour. Handler code reads the
// authenticated user via `UserIDFromContext(ctx)` — identical to the gRPC
// path — so feature packages don't care which transport invoked them.

// publicHTTPPrefixes maps (METHOD, PATH-prefix) to "no auth required".
// Mirrors the gRPC publicMethods list. Invite preview is also public
// (GET /v1/invites/{code}) so users can peek before joining.
type httpMethodPath struct {
	method string
	prefix string
}

var publicHTTPEndpoints = []httpMethodPath{
	{"POST", "/v1/auth/register"},
	{"POST", "/v1/auth/verify-otp"},
	{"POST", "/v1/auth/resend-otp"},
	{"POST", "/v1/auth/login"},
	{"POST", "/v1/auth/refresh"},
	{"POST", "/v1/auth/validate"},
	// Invite preview before join — keyed by invite code in the path.
	{"GET", "/v1/invites/"},
}

func isPublicHTTP(r *http.Request) bool {
	for _, p := range publicHTTPEndpoints {
		if r.Method == p.method && strings.HasPrefix(r.URL.Path, p.prefix) {
			// /v1/invites/{code}/join is NOT public — that's the join op.
			if p.prefix == "/v1/invites/" && strings.HasSuffix(r.URL.Path, "/join") {
				return false
			}
			return true
		}
	}
	return false
}

// HTTPAuthMiddleware validates the Bearer JWT and stuffs the user_id into
// the request context under middleware.UserIDKey — same key the gRPC
// interceptor uses, so handler code is transport-agnostic.
func HTTPAuthMiddleware(secret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isPublicHTTP(r) {
				next.ServeHTTP(w, r)
				return
			}
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				writeJSONError(w, http.StatusUnauthorized, "missing authorization header")
				return
			}
			tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
			token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
				if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, fmt.Errorf("invalid signing method")
				}
				return []byte(secret), nil
			})
			if err != nil || !token.Valid {
				writeJSONError(w, http.StatusUnauthorized, "invalid token")
				return
			}
			claims, ok := token.Claims.(jwt.MapClaims)
			if !ok {
				writeJSONError(w, http.StatusUnauthorized, "invalid token claims")
				return
			}
			userID, ok := claims["sub"].(string)
			if !ok {
				writeJSONError(w, http.StatusUnauthorized, "missing user id in token")
				return
			}
			ctx := context.WithValue(r.Context(), UserIDKey, userID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// HTTPRateLimitMiddleware applies the same sliding-window limits as the
// gRPC interceptor. Keying rules:
//   - /v1/auth/* → client IP (100/min)
//   - message-send endpoints → user_id (30/min)
//   - friend-request endpoints → user_id (10/min)
//   - everything else → user_id (60/min, falls back to IP if unauthed)
func HTTPRateLimitMiddleware(rdb *redis.Client) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identity, rl := resolveHTTPRateLimit(r)
			key := fmt.Sprintf("ratelimit:http:%s:%s:%s", identity, r.Method, r.URL.Path)
			allowed, err := checkRateLimit(r.Context(), rdb, key, rl)
			if err != nil {
				// Fail open when Redis is unavailable — matches gRPC behavior.
				next.ServeHTTP(w, r)
				return
			}
			if !allowed {
				w.Header().Set("Retry-After", fmt.Sprintf("%d", int(rl.window.Seconds())))
				writeJSONError(w, http.StatusTooManyRequests,
					fmt.Sprintf("rate limit exceeded: %d requests per %s", rl.limit, rl.window))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func resolveHTTPRateLimit(r *http.Request) (string, rateLimit) {
	path := r.URL.Path

	// Auth endpoints: keyed by IP, bucket-by-path not bucket-by-user.
	if strings.HasPrefix(path, "/v1/auth/") {
		return httpPeerIP(r), rateLimit{limit: 100, window: time.Minute}
	}

	userID, _ := r.Context().Value(UserIDKey).(string)
	identity := userID
	if identity == "" {
		identity = httpPeerIP(r)
	}

	if r.Method == "POST" && (strings.HasSuffix(path, "/messages") ||
		(strings.HasPrefix(path, "/v1/users/") && strings.HasSuffix(path, "/messages"))) {
		return identity, messageSendLimit
	}
	if r.Method == "POST" && path == "/v1/friends/requests" {
		return identity, friendRequestLimit
	}
	return identity, defaultLimit
}

func httpPeerIP(r *http.Request) string {
	// Honor reverse-proxy headers when present, else fall back to RemoteAddr.
	// X-Forwarded-For may be a comma-separated chain — take the left-most.
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

// HTTPLoggingMiddleware emits a single structured line per request.
// Wraps ResponseWriter to capture status for logging / metrics.
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

// Flush + Hijack pass-through so SSE streaming and any hijackers still work
// when the middleware is in the chain.
func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func writeJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]any{
		"code":    code,
		"message": msg,
	})
}
