package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"

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
//
// The mux is wired to a real *grpc.ClientConn (typically an in-process
// bufconn loopback). This matters: the older RegisterXxxHandlerServer
// path uses an in-process transport that **does not support streaming
// calls**, so SSE endpoints under /v1/stream/* return 501. With a real
// ClientConn the same handlers support unary + server-streaming RPCs
// uniformly, and all gRPC interceptors (auth, rate-limit, validation)
// run on every REST-originated call — one chain, no duplicate HTTP
// middleware required.
//
// Streaming RPCs are exposed as SSE: grpc-gateway emits each streamed
// message as an event through our sseMarshaler (registered for
// text/event-stream). Clients opt in by sending `Accept: text/event-stream`.
func NewGatewayMux(ctx context.Context, conn *grpc.ClientConn) (*runtime.ServeMux, error) {
	mux := runtime.NewServeMux(
		runtime.WithMarshalerOption(runtime.MIMEWildcard, &runtime.JSONPb{}),
		runtime.WithMarshalerOption("text/event-stream", &sseMarshaler{}),
	)

	if err := authv1.RegisterAuthServiceHandler(ctx, mux, conn); err != nil {
		return nil, err
	}
	if err := userv1.RegisterUserServiceHandler(ctx, mux, conn); err != nil {
		return nil, err
	}
	if err := guildv1.RegisterGuildServiceHandler(ctx, mux, conn); err != nil {
		return nil, err
	}
	if err := channelv1.RegisterChannelServiceHandler(ctx, mux, conn); err != nil {
		return nil, err
	}
	if err := messagev1.RegisterMessageServiceHandler(ctx, mux, conn); err != nil {
		return nil, err
	}
	if err := streamv1.RegisterStreamServiceHandler(ctx, mux, conn); err != nil {
		return nil, err
	}
	if err := syncv1.RegisterSyncServiceHandler(ctx, mux, conn); err != nil {
		return nil, err
	}
	if err := presencev1.RegisterPresenceServiceHandler(ctx, mux, conn); err != nil {
		return nil, err
	}
	if err := mediav1.RegisterMediaServiceHandler(ctx, mux, conn); err != nil {
		return nil, err
	}
	if err := voicev1.RegisterVoiceServiceHandler(ctx, mux, conn); err != nil {
		return nil, err
	}
	return mux, nil
}

// NewPublicHTTPHandler returns the root HTTP handler exposing:
//   - REST under the gateway mux (Discord-style paths at /v1/*)
//   - OpenAPI spec at /openapi.json
//   - Swagger UI at /swagger/
//
// The gRPC interceptor chain (auth / rate-limit / validation) runs on
// every REST call via the loopback, so the only HTTP-layer middleware we
// add is structured access logging — URL-level visibility the gRPC log
// (which shows /guild.v1.GuildService/GetGuild) doesn't give us.
// Swagger UI and the spec stay outside the logged path so they remain
// publicly reachable even without a token.
func NewPublicHTTPHandler(gw *runtime.ServeMux) http.Handler {
	logged := middleware.HTTPLoggingMiddleware()(gw)
	mux := http.NewServeMux()
	mux.Handle("/v1/", logged)
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
