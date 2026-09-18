package coreapi

import (
	_ "embed"
	"net/http"
)

// openapiSpec is the hand-maintained OpenAPI 3.0 document for the gateway.
// docs/openapi.json is an identical repo-facing copy; keep them in sync.
//
//go:embed openapi.json
var openapiSpec []byte

func (s *Server) handleOpenAPI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(openapiSpec)
}
