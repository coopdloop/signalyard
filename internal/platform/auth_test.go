package platform

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHashTokenDependsOnSalt(t *testing.T) {
	if HashToken("a", "tok") == HashToken("b", "tok") {
		t.Fatal("hash must depend on salt")
	}
	if HashToken("a", "tok") != HashToken("a", "tok") {
		t.Fatal("hash must be deterministic")
	}
}

func TestExtractToken(t *testing.T) {
	cases := map[string]string{
		"SignalYard k": "k",
		"Splunk k":     "k",
		"Bearer k":     "k",
		"Basic k":      "",
	}
	for header, want := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Authorization", header)
		if got := extractToken(r); got != want {
			t.Errorf("header %q: got %q want %q", header, got, want)
		}
	}
}

func TestMiddlewareRejectsMissingToken(t *testing.T) {
	auth := NewTokenAuth(nil, "salt", "", "")
	called := false
	h := auth.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusUnauthorized || called {
		t.Fatalf("got %d called=%v", w.Code, called)
	}
}

func TestResolveDevAdmin(t *testing.T) {
	auth := NewTokenAuth(nil, "salt", "", "dev-token")
	id, err := auth.Resolve(t.Context(), "dev-token")
	if err != nil {
		t.Fatal(err)
	}
	if id.Kind != "dev-admin" || id.Role != "admin" {
		t.Fatalf("unexpected identity %+v", id)
	}
}

func TestRequireRole(t *testing.T) {
	cases := []struct {
		name     string
		identity Identity
		allowed  []string
		want     int
	}{
		{"admin allowed", Identity{Kind: "user", Role: "admin"}, []string{"admin", "approver"}, http.StatusOK},
		{"approver allowed", Identity{Kind: "user", Role: "approver"}, []string{"admin", "approver"}, http.StatusOK},
		{"viewer rejected", Identity{Kind: "user", Role: "viewer"}, []string{"admin", "approver"}, http.StatusForbidden},
		{"dev-admin bypass", Identity{Kind: "dev-admin", Role: "admin"}, []string{"admin"}, http.StatusOK},
		{"agent token rejected", Identity{Kind: "agent"}, []string{"admin", "approver"}, http.StatusForbidden},
		{"no identity rejected", Identity{}, []string{"admin"}, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			h := RequireRole(tc.allowed...)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
			r := httptest.NewRequest(http.MethodPost, "/", nil)
			r = r.WithContext(context.WithValue(r.Context(), ctxIdentity, tc.identity))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("got %d want %d", w.Code, tc.want)
			}
			if want := tc.want == http.StatusOK; called != want {
				t.Fatalf("handler called=%v want %v", called, want)
			}
		})
	}
}
