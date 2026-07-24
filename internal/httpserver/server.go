package httpserver

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/irazin/3x-ui-subpage/internal/logging"
	"github.com/irazin/3x-ui-subpage/internal/ratelimit"
)

// Server holds the assembled HTTP layer for the subscription service.
type Server struct {
	deps    Deps
	limiter *ratelimit.Limiter
}

// New builds a Server from its dependencies. Call Router() to obtain the
// http.Handler to serve.
func New(deps Deps) *Server {
	return &Server{
		deps:    deps,
		limiter: ratelimit.New(deps.Security.RateLimit.RequestsPerMinute, deps.Security.RateLimit.Burst),
	}
}

// Router builds the full route tree with middleware applied.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()

	r.Use(logging.RequestLogger(s.deps.Logger))
	r.Use(secureHeaders(s.deps.Security.CSP))
	r.Use(s.limiter.Middleware(s.deps.Logger))
	r.Use(gzipCompress)

	r.Get("/healthz", s.handleHealth)

	r.Route("/sub/{subID}", func(r chi.Router) {
		r.Get("/", s.handleSubscription)
		r.Get("/xray", s.handleXray)
		r.Get("/xray.json", s.handleXrayJSON)
		r.Get("/clash", s.handleClash)
		r.Get("/mihomo", s.handleMihomo)
		r.Get("/happ", s.handleHapp)
		r.Get("/incy", s.handleIncy)
		r.Get("/qr.png", s.handleQRPNG)
		r.Get("/qr.svg", s.handleQRSVG)

		r.Route("/link/{index}", func(r chi.Router) {
			r.Get("/", s.handleLink)
			r.Get("/qr.png", s.handleLinkQRPNG)
			r.Get("/qr.svg", s.handleLinkQRSVG)
			r.Get("/config.json", s.handleLinkConfig)
		})
	})

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/subscription/{subID}", s.handleAPISubscription)
		r.Get("/applications", s.handleAPIApplications)
	})

	assetsPrefix := "/assets/" + s.deps.ThemeSlug + "/"
	r.Get(assetsPrefix+"*", s.handleStaticAsset)

	return r
}
