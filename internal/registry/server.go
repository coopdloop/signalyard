package registry

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)

	r.Get("/health", s.handleHealth)

	r.Group(func(r chi.Router) {
		r.Use(s.auth.Middleware)

		r.Get("/v1/schemas", s.handleListSchemas)
		r.Post("/v1/schemas", s.handleCreateSchema)
		r.Get("/v1/schemas/{category}", s.handleGetSchema)
		r.Put("/v1/schemas/{category}", s.handleUpdateSchema)
		r.Delete("/v1/schemas/{category}", s.handleDeleteSchema)
		r.Get("/v1/schemas/{category}/versions", s.handleListVersions)

		r.Get("/v1/categories", s.handleListCategories)

		r.Get("/v1/proposals", s.handleListProposals)
		r.Post("/v1/proposals", s.handleCreateProposal)
		r.Get("/v1/proposals/{proposal_id}", s.handleGetProposal)
		r.Post("/v1/proposals/{proposal_id}/approve", s.handleApproveProposal)
		r.Post("/v1/proposals/{proposal_id}/reject", s.handleRejectProposal)

		r.Get("/v1/agents", s.handleListAgents)
		r.Post("/v1/agents", s.handleCreateAgent)
		r.Get("/v1/agents/{agent_id}", s.handleGetAgent)
		r.Put("/v1/agents/{agent_id}", s.handleUpdateAgent)
		r.Delete("/v1/agents/{agent_id}", s.handleDeleteAgent)
	})

	return r
}
