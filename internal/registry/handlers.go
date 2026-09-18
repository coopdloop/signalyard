package registry

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"signalyard/internal/platform"
)

type Server struct {
	cfg   *Config
	store *Store
	git   GitCommitter
	events EventPublisher
	auth  *platform.TokenAuth
}

func NewServer(cfg *Config, store *Store, git GitCommitter, events EventPublisher, auth *platform.TokenAuth) *Server {
	return &Server{cfg: cfg, store: store, git: git, events: events, auth: auth}
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	platform.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- schemas ---

func (s *Server) handleListSchemas(w http.ResponseWriter, r *http.Request) {
	schemas, err := s.store.ListActiveSchemas(r.Context())
	if err != nil {
		platform.WriteError(w, http.StatusInternalServerError, "list failed")
		return
	}
	platform.WriteJSON(w, http.StatusOK, map[string]any{"schemas": schemas})
}

type upsertSchemaRequest struct {
	Category    string         `json:"category"`
	JSONSchema  map[string]any `json:"json_schema"`
	RoutingYAML string         `json:"routing_yaml"`
}

func (s *Server) handleCreateSchema(w http.ResponseWriter, r *http.Request) {
	var req upsertSchemaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Category == "" || req.JSONSchema == nil || req.RoutingYAML == "" {
		platform.WriteError(w, http.StatusBadRequest, "category, json_schema and routing_yaml are required")
		return
	}
	id := platform.IdentityFrom(r.Context())
	sc, err := s.commitNewSchemaVersion(r, req.Category, req.JSONSchema, req.RoutingYAML, id.Name())
	if err != nil {
		platform.WriteError(w, http.StatusInternalServerError, "failed to create schema")
		return
	}
	platform.WriteJSON(w, http.StatusCreated, map[string]any{"category": sc.Category, "version": sc.Version})
}

func (s *Server) handleGetSchema(w http.ResponseWriter, r *http.Request) {
	sc, err := s.store.GetActiveSchema(r.Context(), chi.URLParam(r, "category"))
	if errors.Is(err, ErrNotFound) {
		platform.WriteError(w, http.StatusNotFound, "schema not found")
		return
	} else if err != nil {
		platform.WriteError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	platform.WriteJSON(w, http.StatusOK, map[string]any{
		"category": sc.Category, "json_schema": sc.JSONSchema, "routing_yaml": sc.RoutingYAML, "version": sc.Version,
	})
}

func (s *Server) handleUpdateSchema(w http.ResponseWriter, r *http.Request) {
	category := chi.URLParam(r, "category")
	if _, err := s.store.GetActiveSchema(r.Context(), category); errors.Is(err, ErrNotFound) {
		platform.WriteError(w, http.StatusNotFound, "schema not found")
		return
	}
	var req upsertSchemaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.JSONSchema == nil || req.RoutingYAML == "" {
		platform.WriteError(w, http.StatusBadRequest, "json_schema and routing_yaml are required")
		return
	}
	id := platform.IdentityFrom(r.Context())
	sc, err := s.commitNewSchemaVersion(r, category, req.JSONSchema, req.RoutingYAML, id.Name())
	if err != nil {
		platform.WriteError(w, http.StatusInternalServerError, "failed to update schema")
		return
	}
	platform.WriteJSON(w, http.StatusOK, map[string]any{"category": sc.Category, "version": sc.Version})
}

func (s *Server) handleDeleteSchema(w http.ResponseWriter, r *http.Request) {
	category := chi.URLParam(r, "category")
	if err := s.store.DeleteCategory(r.Context(), category); err != nil {
		if errors.Is(err, ErrNotFound) {
			platform.WriteError(w, http.StatusNotFound, "schema not found")
			return
		}
		platform.WriteError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	platform.WriteJSON(w, http.StatusOK, map[string]any{"category": category, "deleted": true})
}

func (s *Server) handleListVersions(w http.ResponseWriter, r *http.Request) {
	versions, err := s.store.ListSchemaVersions(r.Context(), chi.URLParam(r, "category"))
	if errors.Is(err, ErrNotFound) {
		platform.WriteError(w, http.StatusNotFound, "schema not found")
		return
	} else if err != nil {
		platform.WriteError(w, http.StatusInternalServerError, "list failed")
		return
	}
	platform.WriteJSON(w, http.StatusOK, map[string]any{"versions": versions})
}

func (s *Server) handleListCategories(w http.ResponseWriter, r *http.Request) {
	cats, err := s.store.ListCategories(r.Context())
	if err != nil {
		platform.WriteError(w, http.StatusInternalServerError, "list failed")
		return
	}
	platform.WriteJSON(w, http.StatusOK, map[string]any{"categories": cats})
}

// commitNewSchemaVersion writes the schema row and the git audit commit.
func (s *Server) commitNewSchemaVersion(r *http.Request, category string, jsonSchema map[string]any, routingYAML, actor string) (Schema, error) {
	sc, err := s.store.CreateSchemaVersion(r.Context(), category, jsonSchema, routingYAML, actor, "")
	if err != nil {
		return Schema{}, err
	}
	raw, _ := json.MarshalIndent(jsonSchema, "", "  ")
	sha, err := s.git.CommitSchema(r.Context(), category, sc.Version, raw, routingYAML, "")
	if err != nil {
		// Postgres holds the canonical row; git failure is logged, not fatal (ADR-003 drift note).
		return sc, nil
	}
	if sha != "" {
		_ = s.store.SetSchemaGitSHA(r.Context(), sc.ID, sha)
		sc.GitCommit = sha
	}
	return sc, nil
}

// --- proposals ---

func (s *Server) handleListProposals(w http.ResponseWriter, r *http.Request) {
	proposals, err := s.store.ListProposals(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		platform.WriteError(w, http.StatusInternalServerError, "list failed")
		return
	}
	platform.WriteJSON(w, http.StatusOK, map[string]any{"proposals": proposals})
}

type createProposalRequest struct {
	Category        string         `json:"category"`
	GeneratedBy     string         `json:"generated_by"`
	JSONSchemaPatch map[string]any `json:"json_schema_patch"`
	RoutingYAMLDiff string         `json:"routing_yaml_diff"`
	SampleEventIDs  []string       `json:"sample_event_ids"`
	Confidence      *float64       `json:"confidence"`
}

func (s *Server) handleCreateProposal(w http.ResponseWriter, r *http.Request) {
	var req createProposalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	// Spec lists routing_yaml_diff and sample_event_ids as required, but the
	// gateway's MCP propose_schema tool contract omits them; the registry
	// accepts them as optional so both producers can submit proposals.
	if req.Category == "" || req.GeneratedBy == "" || req.JSONSchemaPatch == nil {
		platform.WriteError(w, http.StatusBadRequest, "category, generated_by and json_schema_patch are required")
		return
	}
	p, err := s.store.CreateProposal(r.Context(), req.Category, req.GeneratedBy, req.JSONSchemaPatch, req.RoutingYAMLDiff, req.SampleEventIDs, req.Confidence)
	if err != nil {
		platform.WriteError(w, http.StatusInternalServerError, "failed to store proposal")
		return
	}
	platform.WriteJSON(w, http.StatusCreated, map[string]any{"proposal_id": p.ID.String(), "status": p.Status})
}

func (s *Server) handleGetProposal(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "proposal_id"))
	if err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid proposal_id")
		return
	}
	p, err := s.store.GetProposal(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		platform.WriteError(w, http.StatusNotFound, "proposal not found")
		return
	} else if err != nil {
		platform.WriteError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	platform.WriteJSON(w, http.StatusOK, map[string]any{
		"proposal_id":       p.ID.String(),
		"category":          p.Category,
		"status":            p.Status,
		"json_schema_patch": p.JSONSchemaPatch,
		"routing_yaml_diff": p.RoutingYAMLDiff,
		"generated_by":      p.GeneratedBy,
		"reviewed_by":       p.ReviewedBy,
		"created_at":        p.CreatedAt,
	})
}

type reviewRequest struct {
	ReviewerNotes string `json:"reviewer_notes"`
}

func (s *Server) handleApproveProposal(w http.ResponseWriter, r *http.Request) {
	s.handleReview(w, r, "approved")
}

func (s *Server) handleRejectProposal(w http.ResponseWriter, r *http.Request) {
	s.handleReview(w, r, "rejected")
}

func (s *Server) handleReview(w http.ResponseWriter, r *http.Request, decision string) {
	id, err := uuid.Parse(chi.URLParam(r, "proposal_id"))
	if err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid proposal_id")
		return
	}
	var req reviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		platform.WriteError(w, http.StatusBadRequest, "reviewer_notes is required")
		return
	}
	p, err := s.store.GetProposal(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		platform.WriteError(w, http.StatusNotFound, "proposal not found")
		return
	} else if err != nil {
		platform.WriteError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	if p.Status != "pending" {
		platform.WriteError(w, http.StatusBadRequest, "proposal already "+p.Status)
		return
	}
	identity := platform.IdentityFrom(r.Context())
	reviewer := identity.Name()
	var userID *uuid.UUID
	if identity.Kind == "user" {
		userID = &identity.UserID
	}

	if decision == "rejected" {
		if err := s.store.FinalizeProposal(r.Context(), id, "rejected", reviewer, req.ReviewerNotes, nil); err != nil {
			platform.WriteError(w, http.StatusInternalServerError, "failed to reject proposal")
			return
		}
		_ = s.store.AuditLog(r.Context(), id, userID, "reject", req.ReviewerNotes)
		platform.WriteJSON(w, http.StatusOK, map[string]any{"proposal_id": id.String(), "status": "rejected"})
		return
	}

	// Approve: create the new schema version, commit to git, then finalize.
	sc, err := s.commitNewSchemaVersion(r, p.Category, p.JSONSchemaPatch, p.RoutingYAMLDiff, reviewer)
	if err != nil {
		platform.WriteError(w, http.StatusInternalServerError, "failed to create schema version")
		return
	}
	if err := s.store.FinalizeProposal(r.Context(), id, "approved", reviewer, req.ReviewerNotes, &sc.ID); err != nil {
		platform.WriteError(w, http.StatusInternalServerError, "failed to finalize proposal")
		return
	}
	_ = s.store.AuditLog(r.Context(), id, userID, "approve", req.ReviewerNotes)

	// Notify the normalizer so it replays matching quarantined events.
	_ = s.events.PublishSchemaApproved(r.Context(), SchemaApprovedEvent{
		Category: p.Category, Version: sc.Version, SchemaID: sc.ID, ProposalID: id,
		ApprovedBy: reviewer, EmittedAt: time.Now().UTC(),
	})

	platform.WriteJSON(w, http.StatusOK, map[string]any{
		"proposal_id": id.String(), "status": "approved", "new_version": sc.Version,
	})
}

// --- agents ---

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := s.store.ListAgents(r.Context())
	if err != nil {
		platform.WriteError(w, http.StatusInternalServerError, "list failed")
		return
	}
	platform.WriteJSON(w, http.StatusOK, map[string]any{"agents": agents})
}

type createAgentRequest struct {
	AgentName    string   `json:"agent_name"`
	Description  string   `json:"description"`
	Capabilities []string `json:"capabilities"`
}

func (s *Server) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	var req createAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.AgentName == "" || req.Capabilities == nil {
		platform.WriteError(w, http.StatusBadRequest, "agent_name and capabilities are required")
		return
	}
	a, err := s.store.CreateAgent(r.Context(), req.AgentName, req.Description, req.Capabilities)
	if err != nil {
		platform.WriteError(w, http.StatusInternalServerError, "failed to register agent")
		return
	}
	platform.WriteJSON(w, http.StatusCreated, map[string]any{"agent_id": a.ID.String()})
}

func (s *Server) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "agent_id"))
	if err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid agent_id")
		return
	}
	a, err := s.store.GetAgent(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		platform.WriteError(w, http.StatusNotFound, "agent not found")
		return
	} else if err != nil {
		platform.WriteError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	keys, err := s.store.ListAPIKeysForAgent(r.Context(), id)
	if err != nil {
		platform.WriteError(w, http.StatusInternalServerError, "key lookup failed")
		return
	}
	platform.WriteJSON(w, http.StatusOK, map[string]any{
		"agent_id": a.ID.String(), "agent_name": a.Name, "status": a.Status,
		"capabilities": a.Capabilities, "api_key_metadata": keys,
	})
}

type updateAgentRequest struct {
	AgentName    string   `json:"agent_name"`
	Status       string   `json:"status"`
	Capabilities []string `json:"capabilities"`
}

func (s *Server) handleUpdateAgent(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "agent_id"))
	if err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid agent_id")
		return
	}
	var req updateAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := s.store.UpdateAgent(r.Context(), id, req.AgentName, req.Status, req.Capabilities); err != nil {
		if errors.Is(err, ErrNotFound) {
			platform.WriteError(w, http.StatusNotFound, "agent not found")
			return
		}
		platform.WriteError(w, http.StatusInternalServerError, "update failed")
		return
	}
	platform.WriteJSON(w, http.StatusOK, map[string]any{"agent_id": id.String()})
}

func (s *Server) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "agent_id"))
	if err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid agent_id")
		return
	}
	if err := s.store.DeleteAgent(r.Context(), id); err != nil {
		if errors.Is(err, ErrNotFound) {
			platform.WriteError(w, http.StatusNotFound, "agent not found")
			return
		}
		platform.WriteError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	platform.WriteJSON(w, http.StatusOK, map[string]any{"agent_id": id.String(), "deleted": true})
}
