package registry

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"signalyard/internal/platform"
)

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(platform.CORS)

	r.Get("/health", s.handleHealth)

	r.Group(func(r chi.Router) {
		r.Use(s.auth.Middleware)

		// Read routes and machine proposal submissions: any valid token.
		r.Get("/v1/schemas", s.handleListSchemas)
		r.Get("/v1/schemas/{category}", s.handleGetSchema)
		r.Get("/v1/schemas/{category}/versions", s.handleListVersions)
		r.Get("/v1/categories", s.handleListCategories)
		r.Get("/v1/proposals", s.handleListProposals)
		r.Post("/v1/proposals", s.handleCreateProposal)
		r.Get("/v1/proposals/{proposal_id}", s.handleGetProposal)
		r.Get("/v1/audit-log", s.handleListAuditLog)
		r.Get("/v1/agents", s.handleListAgents)
		r.Get("/v1/agents/{agent_id}", s.handleGetAgent)

		// Mutating admin routes: human veto roles only.
		r.Group(func(r chi.Router) {
			r.Use(platform.RequireRole("admin", "approver"))
			r.Post("/v1/schemas", s.handleCreateSchema)
			r.Put("/v1/schemas/{category}", s.handleUpdateSchema)
			r.Delete("/v1/schemas/{category}", s.handleDeleteSchema)
			r.Post("/v1/proposals/{proposal_id}/approve", s.handleApproveProposal)
			r.Post("/v1/proposals/{proposal_id}/reject", s.handleRejectProposal)
			r.Post("/v1/agents", s.handleCreateAgent)
			r.Put("/v1/agents/{agent_id}", s.handleUpdateAgent)
			r.Delete("/v1/agents/{agent_id}", s.handleDeleteAgent)
		})
	})

	return r
}
