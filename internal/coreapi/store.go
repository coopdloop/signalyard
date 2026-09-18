package coreapi

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

type Agent struct {
	ID           uuid.UUID
	Name         string
	Slug         string
	Description  string
	AgentType    string
	Status       string
	ContactEmail string
	CreatedAt    time.Time
}

type APIKey struct {
	ID         uuid.UUID
	AgentID    uuid.UUID
	KeyPrefix  string
	Label      string
	Scopes     []string
	RevokedAt  *time.Time
	LastUsedAt *time.Time
	ExpiresAt  *time.Time
	CreatedAt  time.Time
}

type Event struct {
	ID        uuid.UUID       `json:"event_id"`
	AgentID   *uuid.UUID      `json:"agent_id,omitempty"`
	Category  string          `json:"category"`
	Source    string          `json:"source,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
	Payload   map[string]any  `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

type User struct {
	ID          uuid.UUID
	Email       string
	DisplayName string
	OIDCSubject string
	Role        string
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) Migrate(ctx context.Context, sql string) error {
	_, err := s.pool.Exec(ctx, sql)
	return err
}

func (s *Store) CreateAgent(ctx context.Context, name, email, categoryHint, description string) (Agent, error) {
	var a Agent
	agentType := categoryHint
	if agentType == "" {
		agentType = "custom"
	}
	slug := slugify(name)
	err := s.pool.QueryRow(ctx, `
		INSERT INTO agents (name, slug, description, agent_type, status, contact_email)
		VALUES ($1, $2, $3, $4, 'pending', $5)
		RETURNING id, name, slug, description, agent_type, status, contact_email, created_at`,
		name, slug, description, agentType, email).
		Scan(&a.ID, &a.Name, &a.Slug, &a.Description, &a.AgentType, &a.Status, &a.ContactEmail, &a.CreatedAt)
	if err != nil {
		return Agent{}, fmt.Errorf("insert agent: %w", err)
	}
	return a, nil
}

func (s *Store) GetAgent(ctx context.Context, id uuid.UUID) (Agent, error) {
	var a Agent
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, slug, description, agent_type, status, contact_email, created_at
		FROM agents WHERE id = $1`, id).
		Scan(&a.ID, &a.Name, &a.Slug, &a.Description, &a.AgentType, &a.Status, &a.ContactEmail, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Agent{}, ErrNotFound
	}
	return a, err
}

func (s *Store) CreateAPIKey(ctx context.Context, agentID uuid.UUID, label string, scopes []string, keyHash, keyPrefix string) (APIKey, error) {
	var k APIKey
	err := s.pool.QueryRow(ctx, `
		INSERT INTO api_keys (agent_id, key_hash, key_prefix, label, scopes)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, agent_id, key_prefix, label, scopes, revoked_at, last_used_at, expires_at, created_at`,
		agentID, keyHash, keyPrefix, label, scopes).
		Scan(&k.ID, &k.AgentID, &k.KeyPrefix, &k.Label, &k.Scopes, &k.RevokedAt, &k.LastUsedAt, &k.ExpiresAt, &k.CreatedAt)
	if err != nil {
		return APIKey{}, fmt.Errorf("insert api key: %w", err)
	}
	return k, nil
}

func (s *Store) GetAPIKeyByHash(ctx context.Context, keyHash string) (APIKey, error) {
	var k APIKey
	err := s.pool.QueryRow(ctx, `
		SELECT id, agent_id, key_prefix, label, scopes, revoked_at, last_used_at, expires_at, created_at
		FROM api_keys WHERE key_hash = $1`, keyHash).
		Scan(&k.ID, &k.AgentID, &k.KeyPrefix, &k.Label, &k.Scopes, &k.RevokedAt, &k.LastUsedAt, &k.ExpiresAt, &k.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return APIKey{}, ErrNotFound
	}
	return k, err
}

func (s *Store) GetAPIKeyByID(ctx context.Context, id uuid.UUID) (APIKey, error) {
	var k APIKey
	err := s.pool.QueryRow(ctx, `
		SELECT id, agent_id, key_prefix, label, scopes, revoked_at, last_used_at, expires_at, created_at
		FROM api_keys WHERE id = $1`, id).
		Scan(&k.ID, &k.AgentID, &k.KeyPrefix, &k.Label, &k.Scopes, &k.RevokedAt, &k.LastUsedAt, &k.ExpiresAt, &k.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return APIKey{}, ErrNotFound
	}
	return k, err
}

func (s *Store) RevokeAPIKey(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `UPDATE api_keys SET revoked_at = NOW(), updated_at = NOW() WHERE id = $1 AND revoked_at IS NULL`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) TouchAPIKey(ctx context.Context, id uuid.UUID) {
	_, _ = s.pool.Exec(ctx, `UPDATE api_keys SET last_used_at = NOW() WHERE id = $1`, id)
}

func (s *Store) QueryEvents(ctx context.Context, category string, start, end time.Time, limit int) ([]Event, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	q := `SELECT id, agent_id, category, source, timestamp, payload, created_at FROM events WHERE true`
	args := []any{}
	if category != "" {
		args = append(args, category)
		q += fmt.Sprintf(" AND category = $%d", len(args))
	}
	if !start.IsZero() {
		args = append(args, start)
		q += fmt.Sprintf(" AND timestamp >= $%d", len(args))
	}
	if !end.IsZero() {
		args = append(args, end)
		q += fmt.Sprintf(" AND timestamp <= $%d", len(args))
	}
	args = append(args, limit)
	q += fmt.Sprintf(" ORDER BY timestamp DESC LIMIT $%d", len(args))

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.AgentID, &e.Category, &e.Source, &e.Timestamp, &e.Payload, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) UpsertUserByOIDC(ctx context.Context, email, subject, displayName string) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx, `
		INSERT INTO users (email, display_name, oidc_subject, role, last_login_at)
		VALUES ($1, $2, $3, 'viewer', NOW())
		ON CONFLICT (oidc_subject) DO UPDATE SET last_login_at = NOW(), display_name = EXCLUDED.display_name
		RETURNING id, email, display_name, oidc_subject, role`,
		email, displayName, subject).
		Scan(&u.ID, &u.Email, &u.DisplayName, &u.OIDCSubject, &u.Role)
	if err != nil {
		return User{}, fmt.Errorf("upsert user: %w", err)
	}
	return u, nil
}

func slugify(name string) string {
	out := make([]rune, 0, len(name))
	lastDash := false
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out = append(out, r)
			lastDash = false
		case r >= 'A' && r <= 'Z':
			out = append(out, r+32)
			lastDash = false
		default:
			if !lastDash && len(out) > 0 {
				out = append(out, '-')
				lastDash = true
			}
		}
	}
	s := string(out)
	if s == "" {
		s = "agent"
	}
	// keep slugs unique without a retry loop
	return fmt.Sprintf("%s-%s", s, uuid.NewString()[:8])
}
