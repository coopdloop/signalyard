package normalizer

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

// Envelope is the message shape the gateway publishes on events.ingest.*.
type Envelope struct {
	EventID    string         `json:"event_id"`
	AgentID    string         `json:"agent_id"`
	AgentSlug  string         `json:"agent_slug"`
	Category   string         `json:"category"`
	Source     string         `json:"source"`
	Timestamp  time.Time      `json:"timestamp"`
	Payload    map[string]any `json:"payload"`
	ReceivedAt time.Time      `json:"received_at"`
}

type QuarantineEvent struct {
	EventID    uuid.UUID      `json:"event_id"`
	AgentID    *uuid.UUID     `json:"agent_id,omitempty"`
	Source     string         `json:"source,omitempty"`
	RawPayload map[string]any `json:"raw_payload"`
	Reason     string         `json:"reason"`
	Status     string         `json:"status"`
	Attempts   int            `json:"attempts"`
	ReceivedAt time.Time      `json:"received_at"`
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

func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// InsertEvent writes a validated event into the structured entity store,
// resolving the category + active schema by name.
func (s *Store) InsertEvent(ctx context.Context, env Envelope) error {
	var agentID *uuid.UUID
	if parsed, err := uuid.Parse(env.AgentID); err == nil {
		agentID = &parsed
	}
	var categoryID, schemaID uuid.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT c.id, sc.id FROM categories c
		JOIN schemas sc ON sc.category_id = c.id AND sc.is_active
		WHERE c.name = $1 ORDER BY sc.version DESC LIMIT 1`, env.Category).Scan(&categoryID, &schemaID)
	if err != nil {
		return fmt.Errorf("resolve category/schema: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO events (agent_id, category_id, schema_id, source, normalized_payload, occurred_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		agentID, categoryID, schemaID, env.Source, env.Payload, env.Timestamp)
	return err
}

// Quarantine records an event that failed validation or has no known schema.
// The row id is the envelope's event_id so downstream consumers (classifier,
// proposals, classifier_runs) can join back to it. Idempotent: a re-quarantined
// event (e.g. failed replay) bumps attempts instead of erroring.
func (s *Store) Quarantine(ctx context.Context, env Envelope, reason string) error {
	var agentID *uuid.UUID
	if parsed, err := uuid.Parse(env.AgentID); err == nil {
		agentID = &parsed
	}
	id, err := uuid.Parse(env.EventID)
	if err != nil {
		id = uuid.New()
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO quarantine_events (id, agent_id, source, raw_payload, reason, status)
		VALUES ($1, $2, $3, $4, $5, 'pending')
		ON CONFLICT (id) DO UPDATE SET attempts = quarantine_events.attempts + 1,
			reason = EXCLUDED.reason, updated_at = NOW()`,
		id, agentID, env.Source, env, reason)
	return err
}

func (s *Store) ListQuarantined(ctx context.Context, status string) ([]QuarantineEvent, int, error) {
	if status == "" {
		status = "pending"
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, agent_id, COALESCE(source, ''), raw_payload, COALESCE(reason, ''), status, attempts, created_at
		FROM quarantine_events WHERE status = $1 ORDER BY created_at DESC LIMIT 500`, status)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []QuarantineEvent{}
	for rows.Next() {
		var q QuarantineEvent
		if err := rows.Scan(&q.EventID, &q.AgentID, &q.Source, &q.RawPayload, &q.Reason, &q.Status, &q.Attempts, &q.ReceivedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, q)
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM quarantine_events WHERE status = $1`, status).Scan(&total); err != nil {
		return nil, 0, err
	}
	return out, total, rows.Err()
}

func (s *Store) GetQuarantined(ctx context.Context, id uuid.UUID) (QuarantineEvent, error) {
	var q QuarantineEvent
	err := s.pool.QueryRow(ctx, `
		SELECT id, agent_id, COALESCE(source, ''), raw_payload, COALESCE(reason, ''), status, attempts, created_at
		FROM quarantine_events WHERE id = $1`, id).
		Scan(&q.EventID, &q.AgentID, &q.Source, &q.RawPayload, &q.Reason, &q.Status, &q.Attempts, &q.ReceivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return QuarantineEvent{}, ErrNotFound
	}
	return q, err
}

func (s *Store) QuarantinedByCategory(ctx context.Context, category string) ([]QuarantineEvent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, agent_id, COALESCE(source, ''), raw_payload, COALESCE(reason, ''), status, attempts, created_at
		FROM quarantine_events
		WHERE status = 'pending' AND raw_payload->>'category' = $1
		ORDER BY created_at`, category)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []QuarantineEvent{}
	for rows.Next() {
		var q QuarantineEvent
		if err := rows.Scan(&q.EventID, &q.AgentID, &q.Source, &q.RawPayload, &q.Reason, &q.Status, &q.Attempts, &q.ReceivedAt); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

func (s *Store) MarkReplayed(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE quarantine_events SET status = 'replayed', replayed_at = NOW(), updated_at = NOW() WHERE id = $1`, id)
	return err
}

func (s *Store) BumpAttempts(ctx context.Context, id uuid.UUID, reason string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE quarantine_events SET attempts = attempts + 1, reason = $2, updated_at = NOW() WHERE id = $1`, id, reason)
	return err
}
