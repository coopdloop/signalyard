package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// SchemaEventsStream carries schema lifecycle events for the normalizer (ADR-002/003).
const SchemaEventsStream = "schema_events"

const SubjectSchemaApproved = "schema.approved"

type SchemaApprovedEvent struct {
	Category   string    `json:"category"`
	Version    int       `json:"version"`
	SchemaID   uuid.UUID `json:"schema_id"`
	ProposalID uuid.UUID `json:"proposal_id,omitempty"`
	ApprovedBy string    `json:"approved_by"`
	EmittedAt  time.Time `json:"emitted_at"`
}

type EventPublisher interface {
	PublishSchemaApproved(ctx context.Context, ev SchemaApprovedEvent) error
}

type NATSEventPublisher struct {
	js jetstream.JetStream
}

func NewNATSEventPublisher(ctx context.Context, url string) (*NATSEventPublisher, error) {
	nc, err := nats.Connect(url, nats.Timeout(10*time.Second), nats.Name("schema-registry"))
	if err != nil {
		return nil, fmt.Errorf("connect nats: %w", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("jetstream: %w", err)
	}
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     SchemaEventsStream,
		Subjects: []string{"schema.>"},
	}); err != nil {
		nc.Close()
		return nil, fmt.Errorf("ensure schema_events stream: %w", err)
	}
	return &NATSEventPublisher{js: js}, nil
}

func (p *NATSEventPublisher) PublishSchemaApproved(ctx context.Context, ev SchemaApprovedEvent) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	_, err = p.js.Publish(ctx, SubjectSchemaApproved, data)
	return err
}
