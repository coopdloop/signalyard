package webhook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

type Delivery struct {
	ID             uuid.UUID       `json:"delivery_id"`
	Tool           string          `json:"tool"`
	Status         string          `json:"status"` // forwarded | failed | pending_retry
	SignatureValid bool            `json:"signature_valid"`
	RawPayload     map[string]any  `json:"raw_payload"`
	Normalized     json.RawMessage `json:"normalized_event,omitempty"`
	Error          string          `json:"error,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
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

// SeedSources upserts the four known tool endpoints.
func (s *Store) SeedSources(ctx context.Context, secrets map[string]string) error {
	for tool, secret := range secrets {
		sum := sha256.Sum256([]byte(secret))
		_, err := s.pool.Exec(ctx, `
			INSERT INTO webhook_sources (name, secret_hash, endpoint_path)
			VALUES ($1, $2, $3)
			ON CONFLICT (endpoint_path) DO UPDATE SET secret_hash = EXCLUDED.secret_hash, updated_at = NOW()`,
			tool, hex.EncodeToString(sum[:]), "/webhooks/"+tool)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) sourceID(ctx context.Context, tool string) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT id FROM webhook_sources WHERE name = $1`, tool).Scan(&id)
	return id, err
}

// RecordDelivery inserts a delivery row and returns its id.
func (s *Store) RecordDelivery(ctx context.Context, tool string, signatureValid bool, raw map[string]any, normalized []byte, headers map[string]string) (uuid.UUID, error) {
	srcID, err := s.sourceID(ctx, tool)
	if err != nil {
		return uuid.Nil, err
	}
	body, _ := json.Marshal(map[string]any{"raw": raw, "normalized": json.RawMessage(normalized)})
	hdr, _ := json.Marshal(headers)
	var id uuid.UUID
	err = s.pool.QueryRow(ctx, `
		INSERT INTO webhook_deliveries (webhook_source_id, headers, raw_body, signature_valid)
		VALUES ($1, $2, $3, $4) RETURNING id`, srcID, hdr, body, signatureValid).Scan(&id)
	return id, err
}

func (s *Store) MarkForwarded(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE webhook_deliveries SET http_status = 202, error_message = NULL, updated_at = NOW() WHERE id = $1`, id)
	return err
}

func (s *Store) MarkFailed(ctx context.Context, id uuid.UUID, errMsg string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE webhook_deliveries SET http_status = 502, error_message = $2, updated_at = NOW() WHERE id = $1`, id, errMsg)
	return err
}

const deliveryCols = `d.id, s.name, d.signature_valid, d.raw_body, COALESCE(d.error_message, ''), d.created_at, COALESCE(d.http_status, 0)`

func scanDelivery(row pgx.Row) (Delivery, error) {
	var d Delivery
	var body []byte
	var httpStatus int
	err := row.Scan(&d.ID, &d.Tool, &d.SignatureValid, &body, &d.Error, &d.CreatedAt, &httpStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return Delivery{}, ErrNotFound
	}
	if err != nil {
		return Delivery{}, err
	}
	var parsed struct {
		Raw        map[string]any  `json:"raw"`
		Normalized json.RawMessage `json:"normalized"`
	}
	_ = json.Unmarshal(body, &parsed)
	d.RawPayload = parsed.Raw
	d.Normalized = parsed.Normalized
	switch httpStatus {
	case 202:
		d.Status = "forwarded"
	case 0:
		d.Status = "pending"
	default:
		d.Status = "failed"
	}
	return d, nil
}

func (s *Store) ListDeliveries(ctx context.Context, limit int) ([]Delivery, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+deliveryCols+`
		FROM webhook_deliveries d JOIN webhook_sources s ON s.id = d.webhook_source_id
		ORDER BY d.created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Delivery{}
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) GetDelivery(ctx context.Context, id uuid.UUID) (Delivery, error) {
	return scanDelivery(s.pool.QueryRow(ctx, `
		SELECT `+deliveryCols+`
		FROM webhook_deliveries d JOIN webhook_sources s ON s.id = d.webhook_source_id
		WHERE d.id = $1`, id))
}
