package coreapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// --- idempotency ---

type fakeIdem struct {
	saved map[string]json.RawMessage
}

func (f *fakeIdem) GetIdempotencyResponse(_ context.Context, key string, _ uuid.UUID) (json.RawMessage, error) {
	if r, ok := f.saved[key]; ok {
		return r, nil
	}
	return nil, ErrNotFound
}

func (f *fakeIdem) SaveIdempotencyResponse(_ context.Context, key string, _, _ uuid.UUID, resp json.RawMessage) error {
	if f.saved == nil {
		f.saved = map[string]json.RawMessage{}
	}
	f.saved[key] = resp
	return nil
}

func TestHandleCollectIdempotency(t *testing.T) {
	pub := &fakePublisher{}
	idem := &fakeIdem{}
	s := &Server{cfg: &Config{}, pub: pub, metrics: newMetrics(), idem: idem}
	body := `{"category":"soar_alert","timestamp":"2026-09-18T19:00:00Z","payload":{"alert_id":"A1"}}`

	newReq := func() (*httptest.ResponseRecorder, *http.Request) {
		r := withAgent(httptest.NewRequest(http.MethodPost, "/v1/collect", strings.NewReader(body)))
		r.Header.Set("Idempotency-Key", "key-123")
		return httptest.NewRecorder(), r
	}

	w1, r1 := newReq()
	s.handleCollect(w1, r1)
	if w1.Code != http.StatusCreated {
		t.Fatalf("first request: got %d want 201: %s", w1.Code, w1.Body.String())
	}
	var first map[string]string
	if err := json.Unmarshal(w1.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}

	w2, r2 := newReq()
	s.handleCollect(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("duplicate: got %d want 200", w2.Code)
	}
	var second map[string]string
	if err := json.Unmarshal(w2.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if second["event_id"] != first["event_id"] {
		t.Fatalf("duplicate returned different event_id: %s vs %s", second["event_id"], first["event_id"])
	}
	if len(pub.subjects) != 1 {
		t.Fatalf("duplicate was published again: %d publishes", len(pub.subjects))
	}

	// No idempotency key => normal behavior (new event each time).
	r3 := withAgent(httptest.NewRequest(http.MethodPost, "/v1/collect", strings.NewReader(body)))
	w3 := httptest.NewRecorder()
	s.handleCollect(w3, r3)
	if w3.Code != http.StatusCreated || len(pub.subjects) != 2 {
		t.Fatalf("no-key request: got %d, publishes=%d", w3.Code, len(pub.subjects))
	}
}

// --- rate limiter ---

func TestRateLimiterBucketMath(t *testing.T) {
	now := time.Now()
	l := newRateLimiter(1, 2) // 1 token/s, burst 2
	l.now = func() time.Time { return now }
	id := uuid.New()

	if !l.allow(id) || !l.allow(id) {
		t.Fatal("burst of 2 should be allowed")
	}
	if l.allow(id) {
		t.Fatal("third request within burst should be denied")
	}
	now = now.Add(1500 * time.Millisecond) // refill 1.5 tokens
	if !l.allow(id) {
		t.Fatal("expected refill after 1.5s")
	}
	if l.allow(id) {
		t.Fatal("only 1.5 tokens refilled, second allow should fail")
	}
	// a different agent has its own bucket
	if !l.allow(uuid.New()) {
		t.Fatal("buckets must be per-agent")
	}
}

func TestRateLimitMiddleware(t *testing.T) {
	l := newRateLimiter(1, 1)
	s := &Server{cfg: &Config{}, limiter: l}
	called := 0
	h := s.RateLimit(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called++ }))

	agent := Agent{ID: uuid.New(), Slug: "rate-test-12345678"}
	withFixedAgent := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/v1/collect", nil)
		return r.WithContext(context.WithValue(r.Context(), ctxAgent, agent))
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, withFixedAgent())
	if w.Code != http.StatusOK {
		t.Fatalf("first request limited: %d", w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, withFixedAgent())
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: got %d want 429", w.Code)
	}
	if called != 1 {
		t.Fatalf("handler called %d times", called)
	}
}

// --- agent JWTs ---

func TestAgentTokenRoundTrip(t *testing.T) {
	s := &Server{cfg: &Config{JWTSigningSecret: "test-secret"}}
	agentID := uuid.New()

	token, expires, err := s.issueAgentToken(agentID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if time.Until(expires) < 59*time.Minute {
		t.Fatal("expiry too soon")
	}
	got, err := s.parseAgentToken(token)
	if err != nil {
		t.Fatal(err)
	}
	if got != agentID {
		t.Fatalf("got %s want %s", got, agentID)
	}

	// A session JWT (no kind=agent claim) must not validate as an agent token.
	session, _, err := s.issueSessionToken(User{ID: uuid.New(), Email: "a@b.c", Role: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.parseAgentToken(session); err == nil {
		t.Fatal("session token accepted as agent token")
	}
	// Garbage and wrong-secret tokens rejected.
	if _, err := s.parseAgentToken("not-a-jwt"); err == nil {
		t.Fatal("garbage token accepted")
	}
	other := &Server{cfg: &Config{JWTSigningSecret: "other-secret"}}
	if _, err := other.parseAgentToken(token); err == nil {
		t.Fatal("token signed with different secret accepted")
	}
}

// --- role enforcement ---

func TestRequireRole(t *testing.T) {
	cases := []struct {
		role string
		want int
	}{
		{"admin", http.StatusOK},
		{"approver", http.StatusOK},
		{"viewer", http.StatusForbidden},
		{"", http.StatusForbidden},
	}
	for _, tc := range cases {
		h := RequireRole("admin", "approver")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		r := httptest.NewRequest(http.MethodPost, "/v1/api-keys", nil)
		r = r.WithContext(context.WithValue(r.Context(), ctxUser, User{Role: tc.role}))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("role %q: got %d want %d", tc.role, w.Code, tc.want)
		}
	}
}

// --- openapi ---

func TestOpenAPISpecCoversAllRoutes(t *testing.T) {
	var doc struct {
		Paths map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(openapiSpec, &doc); err != nil {
		t.Fatalf("openapi.json is not valid JSON: %v", err)
	}
	required := []string{
		"/health", "/metrics", "/start-here-agents", "/openapi.json",
		"/register", "/login",
		"/v1/api-keys", "/v1/api-keys/{key_id}", "/v1/tokens", "/v1/collect",
		"/v1/otlp/traces", "/v1/otlp/metrics", "/v1/otlp/logs",
		"/mcp/manifest",
		"/mcp/tools/ingest_event", "/mcp/tools/query_events", "/mcp/tools/propose_schema",
		"/mcp/tools/list_categories", "/mcp/tools/get_manifest",
	}
	for _, path := range required {
		if _, ok := doc.Paths[path]; !ok {
			t.Errorf("missing path %s in openapi.json", path)
		}
	}
	if len(doc.Paths) < 18 {
		t.Errorf("expected at least 18 paths, got %d", len(doc.Paths))
	}
}

func TestHandleOpenAPI(t *testing.T) {
	s := &Server{cfg: &Config{}, metrics: newMetrics()}
	r := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	w := httptest.NewRecorder()
	s.handleOpenAPI(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d", w.Code)
	}
	var doc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal("served document is not valid JSON")
	}
}
