package coreapi

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type ctxKey string

const (
	ctxAgent ctxKey = "agent"
	ctxUser  ctxKey = "user"
)

// GenerateAPIKey returns (plaintext token, key hash, key prefix).
// Token format: sy_<prefix8>_<secret40hex>. Hash is HMAC-SHA256 with HEC_TOKEN_SALT,
// so the plaintext key is never stored.
func GenerateAPIKey(salt string) (token, keyHash, prefix string, err error) {
	buf := make([]byte, 24)
	if _, err = rand.Read(buf); err != nil {
		return "", "", "", err
	}
	prefix = hex.EncodeToString(buf[:4])
	token = "sy_" + prefix + "_" + hex.EncodeToString(buf[4:])
	return token, HashAPIKey(salt, token), prefix, nil
}

func HashAPIKey(salt, token string) string {
	mac := hmac.New(sha256.New, []byte(salt))
	mac.Write([]byte(token))
	return hex.EncodeToString(mac.Sum(nil))
}

func extractToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	for _, scheme := range []string{"SignalYard", "Splunk", "Bearer"} {
		if rest, ok := strings.CutPrefix(h, scheme+" "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return r.Header.Get("X-SignalYard-Token")
}

// RequireAPIKey enforces HEC-style token auth for machine traffic
// (/v1/collect, OTLP endpoints, MCP tools).
func (s *Server) RequireAPIKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := extractToken(r)
		if token == "" {
			writeError(w, http.StatusUnauthorized, "missing token")
			return
		}
		key, err := s.store.GetAPIKeyByHash(r.Context(), HashAPIKey(s.cfg.HECTokenSalt, token))
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				writeError(w, http.StatusUnauthorized, "invalid token")
				return
			}
			writeError(w, http.StatusInternalServerError, "auth lookup failed")
			return
		}
		now := time.Now()
		if key.RevokedAt != nil || (key.ExpiresAt != nil && key.ExpiresAt.Before(now)) {
			writeError(w, http.StatusUnauthorized, "token revoked or expired")
			return
		}
		agent, err := s.store.GetAgent(r.Context(), key.AgentID)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "agent not found for token")
			return
		}
		go s.store.TouchAPIKey(context.Background(), key.ID)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxAgent, agent)))
	})
}

func agentFrom(ctx context.Context) Agent {
	a, _ := ctx.Value(ctxAgent).(Agent)
	return a
}

// RequireSession enforces human auth for dashboard-style management routes
// (API key issuance/revocation). Accepts a session JWT from /login, or the
// DEV_ADMIN_TOKEN escape hatch for local development.
func (s *Server) RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := extractToken(r)
		if token == "" {
			writeError(w, http.StatusUnauthorized, "missing session token")
			return
		}
		if s.cfg.DevAdminToken != "" && token == s.cfg.DevAdminToken {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxUser, User{Role: "admin", Email: "dev-admin@local"})))
			return
		}
		claims := jwt.MapClaims{}
		_, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method")
			}
			return []byte(s.cfg.JWTSigningSecret), nil
		})
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid session token")
			return
		}
		role, _ := claims["role"].(string)
		email, _ := claims["email"].(string)
		sub, _ := claims["sub"].(string)
		uid, _ := uuid.Parse(sub)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxUser, User{ID: uid, Email: email, Role: role})))
	})
}

// verifyOIDCToken validates an OIDC ID token against the configured issuer
// and returns the caller's identity claims.
func (s *Server) verifyOIDCToken(ctx context.Context, rawToken string) (email, subject, name string, err error) {
	if s.cfg.OIDCIssuerURL == "" {
		return "", "", "", errors.New("OIDC is not configured")
	}
	provider, err := oidc.NewProvider(ctx, s.cfg.OIDCIssuerURL)
	if err != nil {
		return "", "", "", fmt.Errorf("oidc provider discovery: %w", err)
	}
	verifier := provider.Verifier(&oidc.Config{ClientID: s.cfg.OIDCClientID})
	idToken, err := verifier.Verify(ctx, rawToken)
	if err != nil {
		return "", "", "", fmt.Errorf("verify id token: %w", err)
	}
	var claims struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return "", "", "", err
	}
	return claims.Email, idToken.Subject, claims.Name, nil
}

func (s *Server) issueSessionToken(u User) (string, time.Time, error) {
	expires := time.Now().Add(8 * time.Hour)
	claims := jwt.MapClaims{
		"sub":   u.ID.String(),
		"email": u.Email,
		"role":  u.Role,
		"exp":   expires.Unix(),
		"iat":   time.Now().Unix(),
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(s.cfg.JWTSigningSecret))
	return token, expires, err
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
