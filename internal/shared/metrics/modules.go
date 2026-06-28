package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/fx"
)

type Route struct {
	Pattern string
	Handler http.Handler
}

type Handler struct {
	http.Handler
}

type handlerParams struct {
	fx.In

	Routes []Route `group:"metricsRoutes"`
}

func NewHandler(p handlerParams) *Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	for _, route := range p.Routes {
		mux.Handle(route.Pattern, route.Handler)
	}
	return &Handler{Handler: mux}
}

var Module = fx.Module("metrics", fx.Provide(NewHandler))
