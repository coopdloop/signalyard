package coreapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestGenerateAPIKeyRoundTrip(t *testing.T) {
	token, hash, prefix, err := GenerateAPIKey("test-salt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, "sy_"+prefix+"_") {
		t.Fatalf("token %q does not embed prefix %q", token, prefix)
	}
	if got := HashAPIKey("test-salt", token); got != hash {
		t.Fatal("hash mismatch for same salt+token")
	}
	if got := HashAPIKey("other-salt", token); got == hash {
		t.Fatal("hash must depend on salt")
	}
}

func TestExtractToken(t *testing.T) {
	cases := map[string]string{
		"SignalYard abc": "abc",
		"Splunk abc":     "abc",
		"Bearer abc":     "abc",
		"Basic abc":      "",
	}
	for header, want := range cases {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r.Header.Set("Authorization", header)
		if got := extractToken(r); got != want {
			t.Errorf("header %q: got %q want %q", header, got, want)
		}
	}
}

type fakePublisher struct {
	subjects []string
	bodies   [][]byte
	err      error
}

func (f *fakePublisher) Publish(_ context.Context, subject string, data []byte) error {
	if f.err != nil {
		return f.err
	}
	f.subjects = append(f.subjects, subject)
	f.bodies = append(f.bodies, data)
	return nil
}

func withAgent(r *http.Request) *http.Request {
	a := Agent{ID: uuid.New(), Slug: "test-agent-12345678"}
	return r.WithContext(context.WithValue(r.Context(), ctxAgent, a))
}

func TestHandleCollectValidation(t *testing.T) {
	s := &Server{cfg: &Config{}, pub: &fakePublisher{}, metrics: newMetrics()}

	bad := []string{
		`{}`,
		`{"category":"x"}`,
		`{"category":"x","timestamp":"2026-09-18T19:00:00Z"}`,
		`not json`,
	}
	for _, body := range bad {
		r := withAgent(httptest.NewRequest(http.MethodPost, "/v1/collect", strings.NewReader(body)))
		w := httptest.NewRecorder()
		s.handleCollect(w, r)
		if w.Code != http.StatusBadRequest {
			t.Errorf("body %s: got %d want 400", body, w.Code)
		}
	}
}

func TestHandleCollectPublishesEnvelope(t *testing.T) {
	pub := &fakePublisher{}
	s := &Server{cfg: &Config{}, pub: pub, metrics: newMetrics()}
	body := `{"category":"soar_alert","timestamp":"2026-09-18T19:00:00Z","payload":{"alert_id":"A1"},"source":"splunk-soar"}`
	r := withAgent(httptest.NewRequest(http.MethodPost, "/v1/collect", strings.NewReader(body)))
	w := httptest.NewRecorder()
	s.handleCollect(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("got %d want 201: %s", w.Code, w.Body.String())
	}
	var resp struct {
		EventID string `json:"event_id"`
		Status  string `json:"status"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != "accepted" || resp.EventID == "" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if len(pub.subjects) != 1 || pub.subjects[0] != "events.ingest.soar_alert" {
		t.Fatalf("unexpected subjects: %v", pub.subjects)
	}
	var env ingestEnvelope
	if err := json.Unmarshal(pub.bodies[0], &env); err != nil {
		t.Fatal(err)
	}
	if env.Category != "soar_alert" || env.Payload["alert_id"] != "A1" || env.EventID != resp.EventID {
		t.Fatalf("bad envelope: %+v", env)
	}
}

func TestHandleOTLP(t *testing.T) {
	pub := &fakePublisher{}
	s := &Server{cfg: &Config{}, pub: pub, metrics: newMetrics()}

	r := httptest.NewRequest(http.MethodPost, "/v1/otlp/traces", strings.NewReader(`{"resourceSpans":[]}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleOTLP("traces")(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("got %d want 201", w.Code)
	}
	if pub.subjects[0] != "events.otlp.traces" {
		t.Fatalf("unexpected subject %q", pub.subjects[0])
	}

	r = httptest.NewRequest(http.MethodPost, "/v1/otlp/traces", strings.NewReader(""))
	w = httptest.NewRecorder()
	s.handleOTLP("traces")(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty body: got %d want 400", w.Code)
	}
}

func TestQueryEventsRejectsInvertedRange(t *testing.T) {
	s := &Server{cfg: &Config{}, metrics: newMetrics()}
	body := `{"start_time":"2026-09-18T20:00:00Z","end_time":"2026-09-18T19:00:00Z"}`
	r := httptest.NewRequest(http.MethodPost, "/mcp/tools/query_events", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleQueryEvents(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("got %d want 400", w.Code)
	}
}

func TestStartHereShape(t *testing.T) {
	s := &Server{cfg: &Config{}, metrics: newMetrics()}
	r := httptest.NewRequest(http.MethodGet, "/start-here-agents", nil)
	w := httptest.NewRecorder()
	s.handleStartHere(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"capability_manifest", "openapi_url", "mcp_manifest", "api_key_instructions", "example_payloads"} {
		if _, ok := resp[key]; !ok {
			t.Errorf("missing key %q in /start-here-agents response", key)
		}
	}
}
