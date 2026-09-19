package coreapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"signalyard/internal/platform"
)

type Server struct {
	cfg     *Config
	store   *Store
	pub     Publisher
	metrics *metrics
	idem    idempotencyStore
	limiter *rateLimiter
}

func NewServer(cfg *Config, store *Store, pub Publisher) *Server {
	return &Server{
		cfg:     cfg,
		store:   store,
		pub:     pub,
		metrics: newMetrics(),
		idem:    store,
		limiter: newRateLimiter(cfg.RateLimitRPS, cfg.RateLimitBurst),
	}
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(platform.CORS)

	// Unauthenticated operational + onboarding endpoints
	r.Get("/health", s.handleHealth)
	r.Method(http.MethodGet, "/metrics", s.metrics.handler())
	r.Get("/start-here-agents", s.handleStartHere)
	r.Get("/openapi.json", s.handleOpenAPI)
	r.Get("/mcp/manifest", s.handleMCPManifest)
	r.Post("/register", s.handleRegister)
	r.Post("/login", s.handleLogin)

	// Human session auth: API key lifecycle management
	r.Group(func(r chi.Router) {
		r.Use(s.RequireSession)
		r.Get("/v1/api-keys/{key_id}", s.handleGetAPIKey)
		// Issuance and revocation are privileged human actions.
		r.Group(func(r chi.Router) {
			r.Use(RequireRole("admin", "approver"))
			r.Post("/v1/api-keys", s.handleCreateAPIKey)
			r.Delete("/v1/api-keys/{key_id}", s.handleDeleteAPIKey)
		})
	})

	// Machine auth: ingestion + MCP tools
	r.Group(func(r chi.Router) {
		r.Use(s.RequireAPIKey)
		r.Post("/v1/tokens", s.handleCreateAgentToken)
		r.Group(func(r chi.Router) {
			r.Use(s.RateLimit)
			r.Post("/v1/collect", s.handleCollect)
			r.Post("/mcp/tools/ingest_event", s.handleCollect)
		})
		r.Post("/v1/otlp/traces", s.handleOTLP("traces"))
		r.Post("/v1/otlp/metrics", s.handleOTLP("metrics"))
		r.Post("/v1/otlp/logs", s.handleOTLP("logs"))
		r.Post("/mcp/tools/query_events", s.handleQueryEvents)
		r.Post("/mcp/tools/propose_schema", s.handleProposeSchema)
		r.Post("/mcp/tools/list_categories", s.handleListCategories)
		r.Post("/mcp/tools/get_manifest", s.handleMCPGetManifest)
	})

	return r
}
