package platform

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ctxKey string

const ctxIdentity ctxKey = "identity"

// Identity is whoever a token resolved to: a machine agent (API key) or a
// human user (session JWT from the gateway's /login).
type Identity struct {
	Kind    string // "agent" | "user" | "dev-admin"
	AgentID uuid.UUID
	UserID  uuid.UUID
	Email   string
	Role    string
}

func (i Identity) Name() string {
	switch {
	case i.Kind == "agent":
		return "agent:" + i.AgentID.String()
	case i.Email != "":
		return i.Email
	default:
		return i.Kind
	}
}

func IdentityFrom(ctx context.Context) Identity {
	id, _ := ctx.Value(ctxIdentity).(Identity)
	return id
}

// TokenAuth validates HEC-style machine API keys (hashed into the shared
// api_keys table) and human session JWTs, plus an optional static dev token.
type TokenAuth struct {
	pool         *pgxpool.Pool
	hecSalt      string
	jwtSecret    string
	devAdminToken string
}

func NewTokenAuth(pool *pgxpool.Pool, hecSalt, jwtSecret, devAdminToken string) *TokenAuth {
	return &TokenAuth{pool: pool, hecSalt: hecSalt, jwtSecret: jwtSecret, devAdminToken: devAdminToken}
}

func HashToken(salt, token string) string {
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

func (a *TokenAuth) Resolve(ctx context.Context, token string) (Identity, error) {
	if a.devAdminToken != "" && token == a.devAdminToken {
		return Identity{Kind: "dev-admin", Role: "admin", Email: "dev-admin@local"}, nil
	}
	// Machine API key?
	var agentID uuid.UUID
	err := a.pool.QueryRow(ctx, `
		SELECT agent_id FROM api_keys
		WHERE key_hash = $1 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > NOW())`,
		HashToken(a.hecSalt, token)).Scan(&agentID)
	if err == nil {
		return Identity{Kind: "agent", AgentID: agentID}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Identity{}, err
	}
	// Human session JWT?
	if a.jwtSecret != "" {
		claims := jwt.MapClaims{}
		if _, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method")
			}
			return []byte(a.jwtSecret), nil
		}); err == nil {
			id := Identity{Kind: "user"}
			id.Email, _ = claims["email"].(string)
			id.Role, _ = claims["role"].(string)
			if sub, _ := claims["sub"].(string); sub != "" {
				id.UserID, _ = uuid.Parse(sub)
			}
			return id, nil
		}
	}
	return Identity{}, errors.New("invalid token")
}

func (a *TokenAuth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := extractToken(r)
		if token == "" {
			WriteError(w, http.StatusUnauthorized, "missing token")
			return
		}
		id, err := a.Resolve(r.Context(), token)
		if err != nil {
			WriteError(w, http.StatusUnauthorized, "invalid token")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxIdentity, id)))
	})
}
