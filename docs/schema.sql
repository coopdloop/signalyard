-- Signal Yard initial schema migration

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- core_api_gateway
CREATE TABLE agents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    slug TEXT NOT NULL,
    description TEXT,
    agent_type TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    contact_email TEXT,
    metadata JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX idx_agents_slug ON agents (slug);
CREATE INDEX idx_agents_agent_type ON agents (agent_type);
CREATE INDEX idx_agents_status ON agents (status);

CREATE TABLE api_keys (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    key_hash TEXT NOT NULL,
    key_prefix TEXT NOT NULL,
    label TEXT,
    scopes TEXT[],
    revoked_at TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_api_keys_agent_id ON api_keys (agent_id);
CREATE UNIQUE INDEX idx_api_keys_key_hash ON api_keys (key_hash);

CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email TEXT NOT NULL,
    display_name TEXT,
    oidc_subject TEXT NOT NULL,
    role TEXT NOT NULL DEFAULT 'viewer',
    last_login_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX idx_users_email ON users (email);
CREATE UNIQUE INDEX idx_users_oidc_subject ON users (oidc_subject);

-- schema_registry_service
CREATE TABLE categories (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    description TEXT,
    example_payload JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX idx_categories_name ON categories (name);

CREATE TABLE schemas (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    category_id UUID NOT NULL REFERENCES categories(id) ON DELETE CASCADE,
    version INTEGER NOT NULL DEFAULT 1,
    json_schema JSONB NOT NULL,
    field_mapping JSONB,
    routing_yaml TEXT,
    git_commit_sha TEXT,
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_by TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_schemas_category_id ON schemas (category_id);
CREATE UNIQUE INDEX idx_schemas_category_version ON schemas (category_id, version);
CREATE INDEX idx_schemas_is_active ON schemas (is_active);

-- normalizer_router
CREATE TABLE quarantine_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id UUID REFERENCES agents(id) ON DELETE SET NULL,
    source TEXT,
    raw_payload JSONB NOT NULL,
    headers JSONB,
    reason TEXT,
    status TEXT NOT NULL DEFAULT 'pending',
    classified_category_id UUID REFERENCES categories(id) ON DELETE SET NULL,
    replayed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_quarantine_events_agent_id ON quarantine_events (agent_id);
CREATE INDEX idx_quarantine_events_status ON quarantine_events (status);
CREATE INDEX idx_quarantine_events_created_at ON quarantine_events (created_at);

CREATE TABLE events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id UUID REFERENCES agents(id) ON DELETE SET NULL,
    category_id UUID NOT NULL REFERENCES categories(id) ON DELETE RESTRICT,
    schema_id UUID REFERENCES schemas(id) ON DELETE SET NULL,
    source TEXT,
    normalized_payload JSONB NOT NULL,
    trace_id TEXT,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_events_agent_id ON events (agent_id);
CREATE INDEX idx_events_category_id ON events (category_id);
CREATE INDEX idx_events_occurred_at ON events (occurred_at);
CREATE INDEX idx_events_trace_id ON events (trace_id);

-- classifier_agent_service
CREATE TABLE schema_proposals (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    quarantine_event_id UUID REFERENCES quarantine_events(id) ON DELETE SET NULL,
    category_id UUID REFERENCES categories(id) ON DELETE SET NULL,
    proposed_category_name TEXT,
    proposed_json_schema JSONB NOT NULL,
    proposed_field_mapping JSONB,
    proposed_routing_yaml TEXT,
    diff TEXT,
    classifier_agent_name TEXT,
    confidence NUMERIC,
    status TEXT NOT NULL DEFAULT 'pending',
    reviewed_by TEXT,
    reviewed_at TIMESTAMPTZ,
    resulting_schema_id UUID REFERENCES schemas(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_schema_proposals_category_id ON schema_proposals (category_id);
CREATE INDEX idx_schema_proposals_status ON schema_proposals (status);
CREATE INDEX idx_schema_proposals_quarantine_event_id ON schema_proposals (quarantine_event_id);

CREATE TABLE classifier_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    quarantine_event_id UUID NOT NULL REFERENCES quarantine_events(id) ON DELETE CASCADE,
    proposal_id UUID REFERENCES schema_proposals(id) ON DELETE SET NULL,
    model_name TEXT,
    input_tokens INTEGER,
    output_tokens INTEGER,
    duration_ms INTEGER,
    status TEXT NOT NULL DEFAULT 'success',
    error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_classifier_runs_quarantine_event_id ON classifier_runs (quarantine_event_id);
CREATE INDEX idx_classifier_runs_proposal_id ON classifier_runs (proposal_id);

-- Approval audit log (schema_registry_service)
CREATE TABLE approval_audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    proposal_id UUID NOT NULL REFERENCES schema_proposals(id) ON DELETE CASCADE,
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    action TEXT NOT NULL,
    notes TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_approval_audit_log_proposal_id ON approval_audit_log (proposal_id);
CREATE INDEX idx_approval_audit_log_user_id ON approval_audit_log (user_id);

-- webhook_adapter_service
CREATE TABLE webhook_sources (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    agent_id UUID REFERENCES agents(id) ON DELETE SET NULL,
    secret_hash TEXT,
    endpoint_path TEXT NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT true,
    config JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_webhook_sources_name ON webhook_sources (name);
CREATE UNIQUE INDEX idx_webhook_sources_endpoint_path ON webhook_sources (endpoint_path);

CREATE TABLE webhook_deliveries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    webhook_source_id UUID NOT NULL REFERENCES webhook_sources(id) ON DELETE CASCADE,
    event_id UUID REFERENCES events(id) ON DELETE SET NULL,
    quarantine_event_id UUID REFERENCES quarantine_events(id) ON DELETE SET NULL,
    http_status INTEGER,
    headers JSONB,
    raw_body JSONB,
    signature_valid BOOLEAN,
    error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_webhook_deliveries_webhook_source_id ON webhook_deliveries (webhook_source_id);
CREATE INDEX idx_webhook_deliveries_event_id ON webhook_deliveries (event_id);
CREATE INDEX idx_webhook_deliveries_created_at ON webhook_deliveries (created_at);
