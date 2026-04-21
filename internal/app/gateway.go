package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/redis/go-redis/v9"

	authv1 "github.com/ananddub/ndiscord_backend/gen/auth/v1"
	channelv1 "github.com/ananddub/ndiscord_backend/gen/channel/v1"
	guildv1 "github.com/ananddub/ndiscord_backend/gen/guild/v1"
	mediav1 "github.com/ananddub/ndiscord_backend/gen/media/v1"
	messagev1 "github.com/ananddub/ndiscord_backend/gen/message/v1"
	"github.com/ananddub/ndiscord_backend/gen/openapi"
	presencev1 "github.com/ananddub/ndiscord_backend/gen/presence/v1"
	streamv1 "github.com/ananddub/ndiscord_backend/gen/stream/v1"
	syncv1 "github.com/ananddub/ndiscord_backend/gen/sync/v1"
	userv1 "github.com/ananddub/ndiscord_backend/gen/user/v1"
	voicev1 "github.com/ananddub/ndiscord_backend/gen/voice/v1"
	"github.com/ananddub/ndiscord_backend/internal/shared/middleware"
)

// NewGatewayMux builds an HTTP mux that fronts all 10 gRPC services over REST.
// Handlers are registered in-process (no loopback dial) so REST calls skip the
// gRPC interceptor chain entirely — auth, rate-limit, and validation must be
// enforced again at the HTTP layer via middleware wrapped around this mux.
//
// Streaming RPCs are exposed as SSE: grpc-gateway emits each message as a
// newline-delimited JSON object over HTTP chunked transfer; the SSE marshaler
// registered below adds the `data: ` prefix and the required response headers.
func NewGatewayMux(ctx context.Context, h *Handlers) (*runtime.ServeMux, error) {
	mux := runtime.NewServeMux(
		runtime.WithMarshalerOption(runtime.MIMEWildcard, &runtime.JSONPb{}),
		runtime.WithMarshalerOption("text/event-stream", &sseMarshaler{}),
	)

	if err := authv1.RegisterAuthServiceHandlerServer(ctx, mux, h.Auth); err != nil {
		return nil, err
	}
	if err := userv1.RegisterUserServiceHandlerServer(ctx, mux, h.User); err != nil {
		return nil, err
	}
	if err := guildv1.RegisterGuildServiceHandlerServer(ctx, mux, h.Guild); err != nil {
		return nil, err
	}
	if err := channelv1.RegisterChannelServiceHandlerServer(ctx, mux, h.Channel); err != nil {
		return nil, err
	}
	if err := messagev1.RegisterMessageServiceHandlerServer(ctx, mux, h.Message); err != nil {
		return nil, err
	}
	if err := streamv1.RegisterStreamServiceHandlerServer(ctx, mux, h.Stream); err != nil {
		return nil, err
	}
	if err := syncv1.RegisterSyncServiceHandlerServer(ctx, mux, h.Sync); err != nil {
		return nil, err
	}
	if err := presencev1.RegisterPresenceServiceHandlerServer(ctx, mux, h.Presence); err != nil {
		return nil, err
	}
	if err := mediav1.RegisterMediaServiceHandlerServer(ctx, mux, h.Media); err != nil {
		return nil, err
	}
	if err := voicev1.RegisterVoiceServiceHandlerServer(ctx, mux, h.Voice); err != nil {
		return nil, err
	}
	return mux, nil
}

// NewPublicHTTPHandler returns the root HTTP handler exposing:
//   - REST under the gateway mux (Discord-style paths at /v1/*)
//   - OpenAPI spec at /openapi.json
//   - Swagger UI at /swagger/
//
// Auth + rate-limit + logging middleware wraps ONLY the /v1/* gateway so
// the Swagger UI and OpenAPI spec stay publicly reachable without a token
// (you need them unauthenticated to discover which endpoints to call).
// The middleware reuses the same context key as the gRPC interceptor so
// feature handlers are transport-agnostic.
func NewPublicHTTPHandler(gw *runtime.ServeMux, jwtSecret string, rdb *redis.Client) http.Handler {
	protectedGateway := middleware.HTTPLoggingMiddleware()(
		middleware.HTTPRateLimitMiddleware(rdb)(
			middleware.HTTPAuthMiddleware(jwtSecret)(gw),
		),
	)
	mux := http.NewServeMux()
	mux.Handle("/v1/", protectedGateway)
	mux.HandleFunc("/openapi.json", serveOpenAPISpec)
	mux.HandleFunc("/swagger/", serveSwaggerUI)
	mux.HandleFunc("/swagger", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/swagger/", http.StatusMovedPermanently)
	})
	return mux
}

// IsGRPCRequest distinguishes gRPC traffic from plain HTTP so the same TCP
// port can host both. gRPC requires HTTP/2 AND the application/grpc content
// type — checking one without the other false-positives on HTTP/2 browsers.
func IsGRPCRequest(r *http.Request) bool {
	return r.ProtoMajor == 2 && strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc")
}

// serveOpenAPISpec serves the embedded spec with a Bearer auth security
// scheme injected so Swagger UI shows the Authorize button. The spec is
// parsed + reserialized once on first hit and cached — cheap, read-only.
var (
	augmentedSpec     []byte
	augmentedSpecOnce sync.Once
)

func serveOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	augmentedSpecOnce.Do(func() {
		var doc map[string]any
		if err := json.Unmarshal(openapi.Spec, &doc); err != nil {
			// If the spec fails to parse we still want to serve something —
			// fall back to the raw bytes rather than error out the UI.
			augmentedSpec = openapi.Spec
			return
		}
		doc["securityDefinitions"] = map[string]any{
			"Bearer": map[string]any{
				"type": "apiKey",
				"name": "Authorization",
				"in":   "header",
				"description": "Paste the access token returned by /v1/auth/login or " +
					"/v1/auth/verify-otp. Format: `Bearer <token>` — Swagger UI will " +
					"add the prefix for you, enter the raw token.",
			},
		}
		doc["security"] = []any{
			map[string]any{"Bearer": []any{}},
		}
		out, err := json.Marshal(doc)
		if err != nil {
			augmentedSpec = openapi.Spec
			return
		}
		augmentedSpec = out
	})
	w.Header().Set("Content-Type", "application/json")
	w.Write(augmentedSpec)
}

const swaggerHTML = `<!DOCTYPE html>
<html>
<head>
  <title>ndiscord API</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    window.ui = SwaggerUIBundle({
      url: '/openapi.json',
      dom_id: '#swagger-ui',
      deepLinking: true,
      persistAuthorization: true,
      requestInterceptor: function (req) {
        // Swagger UI passes the apiKey value verbatim; tack on the Bearer
        // prefix so users can paste the raw JWT without worrying about it.
        var auth = req.headers['Authorization'] || req.headers['authorization'];
        if (auth && !/^Bearer /i.test(auth)) {
          req.headers['Authorization'] = 'Bearer ' + auth;
        }
        return req;
      },
    });
  </script>
</body>
</html>`

func serveSwaggerUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(swaggerHTML))
}
