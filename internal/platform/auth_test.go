package platform

import (
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
