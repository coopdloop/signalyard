package coreapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// --- operational ---

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- onboarding ---

func (s *Server) handleStartHere(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"capability_manifest":  capabilityManifest(),
		"openapi_url":          "/openapi.json",
		"mcp_manifest":         mcpManifest(),
		"api_key_instructions": apiKeyInstructions(),
		"example_payloads":     examplePayloads(),
	})
}

func (s *Server) handleMCPManifest(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, mcpManifest())
}

func (s *Server) handleMCPGetManifest(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"capability_manifest": capabilityManifest(),
		"mcp_manifest":        mcpManifest(),
	})
}

// --- auth ---

type registerRequest struct {
	AgentName    string `json:"agent_name"`
	ContactEmail string `json:"contact_email"`
	CategoryHint string `json:"category_hint"`
	Description  string `json:"description"`
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.AgentName == "" || req.ContactEmail == "" {
		writeError(w, http.StatusBadRequest, "agent_name and contact_email are required")
		return
	}
	agent, err := s.store.CreateAgent(r.Context(), req.AgentName, req.ContactEmail, req.CategoryHint, req.Description)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to register agent")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{
		"agent_id": agent.ID.String(),
		"status":   agent.Status,
	})
}

type loginRequest struct {
	OIDCToken string `json:"oidc_token"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.OIDCToken == "" {
		writeError(w, http.StatusBadRequest, "oidc_token is required")
		return
	}
	email, subject, name, err := s.verifyOIDCToken(r.Context(), req.OIDCToken)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid OIDC token")
		return
	}
	user, err := s.store.UpsertUserByOIDC(r.Context(), email, subject, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create session")
		return
	}
	token, expires, err := s.issueSessionToken(user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to issue session")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"session_token": token,
		"expires_at":    expires.UTC().Format(time.RFC3339),
	})
}

// --- API key lifecycle (human session auth) ---

type createAPIKeyRequest struct {
	AgentID string   `json:"agent_id"`
	Label   string   `json:"label"`
	Scopes  []string `json:"scopes"`
}

func (s *Server) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	var req createAPIKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	agentID, err := uuid.Parse(req.AgentID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "valid agent_id is required")
		return
	}
	if _, err := s.store.GetAgent(r.Context(), agentID); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusBadRequest, "agent not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "agent lookup failed")
		return
	}
	token, hash, prefix, err := GenerateAPIKey(s.cfg.HECTokenSalt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate key")
		return
	}
	key, err := s.store.CreateAPIKey(r.Context(), agentID, req.Label, req.Scopes, hash, prefix)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store key")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{
		"api_key":   token,
		"key_id":    key.ID.String(),
		"agent_id":  agentID.String(),
		"issued_at": key.CreatedAt.UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleGetAPIKey(w http.ResponseWriter, r *http.Request) {
	keyID, err := uuid.Parse(chi.URLParam(r, "key_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid key_id")
		return
	}
	key, err := s.store.GetAPIKeyByID(r.Context(), keyID)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "key not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"key_id":     key.ID.String(),
		"agent_id":   key.AgentID.String(),
		"key_prefix": key.KeyPrefix,
		"scopes":     key.Scopes,
		"created_at": key.CreatedAt.UTC().Format(time.RFC3339),
		"revoked":    key.RevokedAt != nil,
	})
}

func (s *Server) handleDeleteAPIKey(w http.ResponseWriter, r *http.Request) {
	keyID, err := uuid.Parse(chi.URLParam(r, "key_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid key_id")
		return
	}
	if err := s.store.RevokeAPIKey(r.Context(), keyID); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "key not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "revoke failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key_id": keyID.String(), "revoked": true})
}

// --- ingestion (machine auth) ---

type collectRequest struct {
	Category  string         `json:"category"`
	Timestamp time.Time      `json:"timestamp"`
	Payload   map[string]any `json:"payload"`
	Source    string         `json:"source"`
}

type ingestEnvelope struct {
	EventID    string         `json:"event_id"`
	AgentID    string         `json:"agent_id"`
	AgentSlug  string         `json:"agent_slug"`
	Category   string         `json:"category"`
	Source     string         `json:"source"`
	Timestamp  time.Time      `json:"timestamp"`
	Payload    map[string]any `json:"payload"`
	ReceivedAt time.Time      `json:"received_at"`
}

// handleCollect serves POST /v1/collect and POST /mcp/tools/ingest_event.
func (s *Server) handleCollect(w http.ResponseWriter, r *http.Request) {
	agent := agentFrom(r.Context())
	idemKey := r.Header.Get("Idempotency-Key")
	if idemKey != "" && s.idem != nil {
		existing, err := s.idem.GetIdempotencyResponse(r.Context(), idemKey, agent.ID)
		if err == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(existing)
			return
		}
		if !errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusInternalServerError, "idempotency lookup failed")
			return
		}
	}
	var req collectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed event payload")
		return
	}
	if req.Category == "" || req.Timestamp.IsZero() || req.Payload == nil {
		writeError(w, http.StatusBadRequest, "category, timestamp and payload are required")
		return
	}
	env := ingestEnvelope{
		EventID:    uuid.NewString(),
		AgentID:    agent.ID.String(),
		AgentSlug:  agent.Slug,
		Category:   req.Category,
		Source:     req.Source,
		Timestamp:  req.Timestamp.UTC(),
		Payload:    req.Payload,
		ReceivedAt: time.Now().UTC(),
	}
	data, err := json.Marshal(env)
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed event payload")
		return
	}
	if err := s.pub.Publish(r.Context(), "events.ingest."+req.Category, data); err != nil {
		writeError(w, http.StatusServiceUnavailable, "ingestion backend unavailable")
		return
	}
	s.metrics.ingestedEvents.WithLabelValues(req.Category, "event").Inc()
	resp := map[string]string{"event_id": env.EventID, "status": "accepted"}
	if idemKey != "" && s.idem != nil {
		if raw, err := json.Marshal(resp); err == nil {
			if eventID, err := uuid.Parse(env.EventID); err == nil {
				_ = s.idem.SaveIdempotencyResponse(r.Context(), idemKey, agent.ID, eventID, raw)
			}
		}
	}
	writeJSON(w, http.StatusCreated, resp)
}

// --- agent tokens ---

type createAgentTokenRequest struct {
	TTLSeconds int `json:"ttl_seconds"`
}

// handleCreateAgentToken exchanges an API key for a short-lived agent JWT.
func (s *Server) handleCreateAgentToken(w http.ResponseWriter, r *http.Request) {
	var req createAgentTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	ttl := 3600
	if req.TTLSeconds != 0 {
		ttl = req.TTLSeconds
	}
	if ttl < 1 || ttl > 86400 {
		writeError(w, http.StatusBadRequest, "ttl_seconds must be between 1 and 86400")
		return
	}
	agent := agentFrom(r.Context())
	token, expires, err := s.issueAgentToken(agent.ID, time.Duration(ttl)*time.Second)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to issue token")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{
		"token":      token,
		"expires_at": expires.UTC().Format(time.RFC3339),
	})
}

// handleOTLP serves the OTLP/HTTP passthrough endpoints. The raw body is
// published verbatim to the ingestion stream; the normalizer owns parsing.
func (s *Server) handleOTLP(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
		if err != nil || len(body) == 0 {
			writeError(w, http.StatusBadRequest, "invalid OTLP payload")
			return
		}
		ct := r.Header.Get("Content-Type")
		if ct != "" && ct != "application/json" && ct != "application/x-protobuf" {
			writeError(w, http.StatusBadRequest, "unsupported OTLP content type")
			return
		}
		if err := s.pub.Publish(r.Context(), "events.otlp."+kind, body); err != nil {
			writeError(w, http.StatusServiceUnavailable, "ingestion backend unavailable")
			return
		}
		// Forward to the OTel Collector when configured (OTLP/HTTP).
		if s.cfg.OTELCollectorEndpoint != "" {
			if err := s.forwardOTLP(r.Context(), kind, ct, body); err != nil {
				writeError(w, http.StatusBadGateway, "otel collector forward failed")
				return
			}
		}
		s.metrics.ingestedEvents.WithLabelValues("otlp_"+kind, "otlp").Inc()
		writeJSON(w, http.StatusCreated, map[string]string{"status": "accepted"})
	}
}

// --- MCP tools ---

type queryEventsRequest struct {
	Category  string    `json:"category"`
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
	Limit     int       `json:"limit"`
}

func (s *Server) handleQueryEvents(w http.ResponseWriter, r *http.Request) {
	var req queryEventsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid query")
		return
	}
	if !req.StartTime.IsZero() && !req.EndTime.IsZero() && req.EndTime.Before(req.StartTime) {
		writeError(w, http.StatusBadRequest, "end_time must be after start_time")
		return
	}
	events, err := s.store.QueryEvents(r.Context(), req.Category, req.StartTime, req.EndTime, req.Limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if events == nil {
		events = []Event{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

type proposeSchemaRequest struct {
	Category        string         `json:"category"`
	JSONSchema      map[string]any `json:"json_schema"`
	RoutingYAMLDiff string         `json:"routing_yaml_diff"`
}

// handleProposeSchema forwards proposals to schema_registry_service (Phase 2).
func (s *Server) handleProposeSchema(w http.ResponseWriter, r *http.Request) {
	var req proposeSchemaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid proposal payload")
		return
	}
	if req.Category == "" || req.JSONSchema == nil {
		writeError(w, http.StatusBadRequest, "category and json_schema are required")
		return
	}
	if s.cfg.SchemaRegistryURL == "" {
		writeError(w, http.StatusServiceUnavailable, "schema registry not configured")
		return
	}
	// Translate the MCP tool contract into the registry's proposal contract.
	agent := agentFrom(r.Context())
	proposal := map[string]any{
		"category":          req.Category,
		"json_schema_patch": req.JSONSchema,
		"routing_yaml_diff": req.RoutingYAMLDiff,
		"generated_by":      "mcp-agent:" + agent.Slug,
		"sample_event_ids":  []string{},
	}
	status, body, err := s.proxyToRegistry(r, http.MethodPost, "/v1/proposals", proposal)
	if err != nil {
		writeError(w, http.StatusBadGateway, "schema registry unreachable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// handleListCategories proxies to the schema registry; when the registry is
// not yet deployed (Phase 1), it returns an empty list rather than failing.
func (s *Server) handleListCategories(w http.ResponseWriter, r *http.Request) {
	if s.cfg.SchemaRegistryURL == "" {
		writeJSON(w, http.StatusOK, map[string]any{"categories": []any{}})
		return
	}
	status, body, err := s.proxyToRegistry(r, http.MethodGet, "/v1/categories", nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, "schema registry unreachable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// forwardOTLP posts the raw OTLP payload to the collector's OTLP/HTTP receiver.
// The endpoint may be host:port or a full base URL.
func (s *Server) forwardOTLP(ctx context.Context, kind, contentType string, body []byte) error {
	base := s.cfg.OTELCollectorEndpoint
	if !strings.HasPrefix(base, "http") {
		base = "http://" + base
	}
	if contentType == "" {
		contentType = "application/json"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/"+kind, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("collector returned %d", resp.StatusCode)
	}
	return nil
}

func (s *Server) proxyToRegistry(r *http.Request, method, path string, payload any) (int, []byte, error) {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(r.Context(), method, s.cfg.SchemaRegistryURL+path, body)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if auth := r.Header.Get("Authorization"); auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return resp.StatusCode, out, err
}
