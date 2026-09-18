package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("conflict")

type Category struct {
	ID          uuid.UUID `json:"-"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Version     int       `json:"current_version"`
	CreatedAt   time.Time `json:"created_at"`
}

type Schema struct {
	ID          uuid.UUID      `json:"schema_id"`
	Category    string         `json:"category"`
	Version     int            `json:"version"`
	JSONSchema  map[string]any `json:"json_schema"`
	RoutingYAML string         `json:"routing_yaml"`
	GitCommit   string         `json:"git_commit_sha,omitempty"`
	IsActive    bool           `json:"is_active"`
	CreatedBy   string         `json:"created_by,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
}

type Proposal struct {
	ID              uuid.UUID      `json:"proposal_id"`
	Category        string         `json:"category"`
	Status          string         `json:"status"`
	GeneratedBy     string         `json:"generated_by,omitempty"`
	JSONSchemaPatch map[string]any `json:"json_schema_patch,omitempty"`
	RoutingYAMLDiff string         `json:"routing_yaml_diff,omitempty"`
	Confidence      *float64       `json:"confidence,omitempty"`
	ReviewedBy      string         `json:"reviewed_by,omitempty"`
	ReviewerNotes   string         `json:"reviewer_notes,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
}

type AgentRecord struct {
	ID           uuid.UUID `json:"agent_id"`
	Name         string    `json:"agent_name"`
	Slug         string    `json:"slug"`
	Description  string    `json:"description"`
	AgentType    string    `json:"agent_type"`
	Status       string    `json:"status"`
	ContactEmail string    `json:"contact_email,omitempty"`
	Capabilities []string  `json:"capabilities"`
	CreatedAt    time.Time `json:"created_at"`
}

type APIKeyMeta struct {
	KeyID     uuid.UUID `json:"key_id"`
	KeyPrefix string    `json:"key_prefix"`
	Label     string    `json:"label,omitempty"`
	Scopes    []string  `json:"scopes"`
	Revoked   bool      `json:"revoked"`
	CreatedAt time.Time `json:"created_at"`
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

// --- categories ---

func (s *Store) ListCategories(ctx context.Context) ([]Category, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.name, COALESCE(c.description, ''), c.created_at,
		       COALESCE((SELECT MAX(version) FROM schemas sc WHERE sc.category_id = c.id AND sc.is_active), 0)
		FROM categories c ORDER BY c.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Category{}
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.Name, &c.Description, &c.CreatedAt, &c.Version); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// upsertCategory returns the category id, creating it if needed.
func (s *Store) upsertCategory(ctx context.Context, tx pgx.Tx, name string) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO categories (name) VALUES ($1)
		ON CONFLICT (name) DO UPDATE SET updated_at = NOW()
		RETURNING id`, name).Scan(&id)
	return id, err
}

// --- schemas ---

// CreateSchemaVersion inserts a new active version for a category and
// deactivates prior versions. Returns the new schema row.
func (s *Store) CreateSchemaVersion(ctx context.Context, category string, jsonSchema map[string]any, routingYAML, createdBy, gitSHA string) (Schema, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Schema{}, err
	}
	defer tx.Rollback(ctx)

	catID, err := s.upsertCategory(ctx, tx, category)
	if err != nil {
		return Schema{}, fmt.Errorf("upsert category: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE schemas SET is_active = false, updated_at = NOW() WHERE category_id = $1`, catID); err != nil {
		return Schema{}, err
	}
	var sc Schema
	err = tx.QueryRow(ctx, `
		INSERT INTO schemas (category_id, version, json_schema, routing_yaml, git_commit_sha, is_active, created_by)
		VALUES ($1, COALESCE((SELECT MAX(version) FROM schemas WHERE category_id = $1), 0) + 1, $2, $3, $4, true, $5)
		RETURNING id, version, json_schema, routing_yaml, is_active, created_by, created_at`,
		catID, jsonSchema, routingYAML, nullStr(gitSHA), createdBy).
		Scan(&sc.ID, &sc.Version, &sc.JSONSchema, &sc.RoutingYAML, &sc.IsActive, &sc.CreatedBy, &sc.CreatedAt)
	if err != nil {
		return Schema{}, fmt.Errorf("insert schema: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Schema{}, err
	}
	sc.Category = category
	sc.GitCommit = gitSHA
	return sc, nil
}

func (s *Store) SetSchemaGitSHA(ctx context.Context, schemaID uuid.UUID, sha string) error {
	_, err := s.pool.Exec(ctx, `UPDATE schemas SET git_commit_sha = $1 WHERE id = $2`, sha, schemaID)
	return err
}

const schemaCols = `sc.id, c.name, sc.version, sc.json_schema, sc.routing_yaml,
	COALESCE(sc.git_commit_sha, ''), sc.is_active, COALESCE(sc.created_by, ''), sc.created_at`

func scanSchema(row pgx.Row) (Schema, error) {
	var sc Schema
	err := row.Scan(&sc.ID, &sc.Category, &sc.Version, &sc.JSONSchema, &sc.RoutingYAML, &sc.GitCommit, &sc.IsActive, &sc.CreatedBy, &sc.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Schema{}, ErrNotFound
	}
	return sc, err
}

func (s *Store) GetActiveSchema(ctx context.Context, category string) (Schema, error) {
	return scanSchema(s.pool.QueryRow(ctx, `
		SELECT `+schemaCols+`
		FROM schemas sc JOIN categories c ON c.id = sc.category_id
		WHERE c.name = $1 AND sc.is_active ORDER BY sc.version DESC LIMIT 1`, category))
}

func (s *Store) ListActiveSchemas(ctx context.Context) ([]Schema, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+schemaCols+`
		FROM schemas sc JOIN categories c ON c.id = sc.category_id
		WHERE sc.is_active ORDER BY c.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Schema{}
	for rows.Next() {
		sc, err := scanSchema(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

func (s *Store) ListSchemaVersions(ctx context.Context, category string) ([]Schema, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+schemaCols+`
		FROM schemas sc JOIN categories c ON c.id = sc.category_id
		WHERE c.name = $1 ORDER BY sc.version DESC`, category)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Schema{}
	for rows.Next() {
		sc, err := scanSchema(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	if len(out) == 0 {
		return nil, ErrNotFound
	}
	return out, rows.Err()
}

func (s *Store) DeleteCategory(ctx context.Context, category string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM categories WHERE name = $1`, category)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- proposals ---

func (s *Store) CreateProposal(ctx context.Context, category, generatedBy string, patch map[string]any, routingDiff string, sampleEventIDs []string, confidence *float64) (Proposal, error) {
	var quarantineID *uuid.UUID
	for _, id := range sampleEventIDs {
		if parsed, err := uuid.Parse(id); err == nil {
			quarantineID = &parsed
			break
		}
	}
	var p Proposal
	err := s.pool.QueryRow(ctx, `
		INSERT INTO schema_proposals
			(quarantine_event_id, proposed_category_name, proposed_json_schema, proposed_routing_yaml, classifier_agent_name, confidence)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, proposed_category_name, status, classifier_agent_name, confidence, created_at`,
		quarantineID, category, patch, routingDiff, generatedBy, confidence).
		Scan(&p.ID, &p.Category, &p.Status, &p.GeneratedBy, &p.Confidence, &p.CreatedAt)
	if err != nil {
		return Proposal{}, fmt.Errorf("insert proposal: %w", err)
	}
	return p, nil
}

const proposalCols = `id, proposed_category_name, status, COALESCE(classifier_agent_name, ''),
	proposed_json_schema, COALESCE(proposed_routing_yaml, ''), confidence,
	COALESCE(reviewed_by, ''), COALESCE(diff, ''), created_at`

func scanProposal(row pgx.Row) (Proposal, error) {
	var p Proposal
	err := row.Scan(&p.ID, &p.Category, &p.Status, &p.GeneratedBy, &p.JSONSchemaPatch, &p.RoutingYAMLDiff, &p.Confidence, &p.ReviewedBy, &p.ReviewerNotes, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Proposal{}, ErrNotFound
	}
	return p, err
}

func (s *Store) ListProposals(ctx context.Context, status string) ([]Proposal, error) {
	q := `SELECT ` + proposalCols + ` FROM schema_proposals`
	args := []any{}
	if status != "" {
		args = append(args, status)
		q += ` WHERE status = $1`
	}
	q += ` ORDER BY created_at DESC`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Proposal{}
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetProposal(ctx context.Context, id uuid.UUID) (Proposal, error) {
	return scanProposal(s.pool.QueryRow(ctx, `SELECT `+proposalCols+` FROM schema_proposals WHERE id = $1`, id))
}

// FinalizeProposal marks a proposal approved/rejected with reviewer metadata.
func (s *Store) FinalizeProposal(ctx context.Context, id uuid.UUID, status, reviewer, notes string, resultingSchemaID *uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE schema_proposals
		SET status = $2, reviewed_by = $3, diff = $4, reviewed_at = NOW(), resulting_schema_id = $5, updated_at = NOW()
		WHERE id = $1 AND status = 'pending'`,
		id, status, reviewer, notes, resultingSchemaID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrConflict // not pending (or missing)
	}
	return nil
}

type AuditEntry struct {
	ID         uuid.UUID  `json:"id"`
	ProposalID uuid.UUID  `json:"proposal_id"`
	Category   string     `json:"category"`
	UserID     *uuid.UUID `json:"user_id"`
	Action     string     `json:"action"`
	Notes      string     `json:"notes"`
	CreatedAt  time.Time  `json:"created_at"`
}

// ListAuditLog returns approval audit entries, newest first (max 200).
func (s *Store) ListAuditLog(ctx context.Context, proposalID *uuid.UUID) ([]AuditEntry, error) {
	q := `SELECT a.id, a.proposal_id, COALESCE(p.proposed_category_name, ''), a.user_id,
			a.action, COALESCE(a.notes, ''), a.created_at
		FROM approval_audit_log a
		LEFT JOIN schema_proposals p ON p.id = a.proposal_id`
	args := []any{}
	if proposalID != nil {
		args = append(args, *proposalID)
		q += ` WHERE a.proposal_id = $1`
	}
	q += ` ORDER BY a.created_at DESC LIMIT 200`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.ProposalID, &e.Category, &e.UserID, &e.Action, &e.Notes, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) AuditLog(ctx context.Context, proposalID uuid.UUID, userID *uuid.UUID, action, notes string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO approval_audit_log (proposal_id, user_id, action, notes) VALUES ($1, $2, $3, $4)`,
		proposalID, userID, action, notes)
	return err
}

// --- agents ---

func (s *Store) ListAgents(ctx context.Context) ([]AgentRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, slug, COALESCE(description, ''), agent_type, status, COALESCE(contact_email, ''), created_at
		FROM agents ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AgentRecord{}
	for rows.Next() {
		var a AgentRecord
		if err := rows.Scan(&a.ID, &a.Name, &a.Slug, &a.Description, &a.AgentType, &a.Status, &a.ContactEmail, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) CreateAgent(ctx context.Context, name, description string, capabilities []string) (AgentRecord, error) {
	meta, _ := json.Marshal(map[string]any{"capabilities": capabilities})
	var a AgentRecord
	err := s.pool.QueryRow(ctx, `
		INSERT INTO agents (name, slug, description, agent_type, status, metadata)
		VALUES ($1, $2, $3, 'custom', 'active', $4)
		RETURNING id, name, slug, COALESCE(description, ''), agent_type, status, created_at`,
		name, fmt.Sprintf("%s-%s", name, uuid.NewString()[:8]), description, meta).
		Scan(&a.ID, &a.Name, &a.Slug, &a.Description, &a.AgentType, &a.Status, &a.CreatedAt)
	if err != nil {
		return AgentRecord{}, fmt.Errorf("insert agent: %w", err)
	}
	a.Capabilities = capabilities
	return a, nil
}

func (s *Store) GetAgent(ctx context.Context, id uuid.UUID) (AgentRecord, error) {
	var a AgentRecord
	var meta []byte
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, slug, COALESCE(description, ''), agent_type, status, COALESCE(contact_email, ''), COALESCE(metadata, '{}'::jsonb), created_at
		FROM agents WHERE id = $1`, id).
		Scan(&a.ID, &a.Name, &a.Slug, &a.Description, &a.AgentType, &a.Status, &a.ContactEmail, &meta, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return AgentRecord{}, ErrNotFound
	}
	if err != nil {
		return AgentRecord{}, err
	}
	var m struct {
		Capabilities []string `json:"capabilities"`
	}
	_ = json.Unmarshal(meta, &m)
	a.Capabilities = m.Capabilities
	return a, nil
}

func (s *Store) UpdateAgent(ctx context.Context, id uuid.UUID, name, status string, capabilities []string) error {
	meta, _ := json.Marshal(map[string]any{"capabilities": capabilities})
	tag, err := s.pool.Exec(ctx, `
		UPDATE agents SET name = COALESCE(NULLIF($2, ''), name),
			status = COALESCE(NULLIF($3, ''), status), metadata = $4, updated_at = NOW()
		WHERE id = $1`, id, name, status, meta)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteAgent(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM agents WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListAPIKeysForAgent(ctx context.Context, agentID uuid.UUID) ([]APIKeyMeta, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, key_prefix, COALESCE(label, ''), COALESCE(scopes, '{}'), revoked_at IS NOT NULL, created_at
		FROM api_keys WHERE agent_id = $1 ORDER BY created_at DESC`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIKeyMeta{}
	for rows.Next() {
		var k APIKeyMeta
		if err := rows.Scan(&k.KeyID, &k.KeyPrefix, &k.Label, &k.Scopes, &k.Revoked, &k.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func nullStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
