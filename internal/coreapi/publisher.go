package coreapi

import (
	"context"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel"

	"signalyard/internal/platform"
)

// IngestStream is the durable buffer between ingestion and normalization (ADR-002).
const IngestStream = "ingest"

type Publisher interface {
	Publish(ctx context.Context, subject string, data []byte) error
}

type NATSPublisher struct {
	js jetstream.JetStream
}

func NewNATSPublisher(ctx context.Context, url string) (*NATSPublisher, error) {
	nc, err := nats.Connect(url, nats.Timeout(10*time.Second), nats.Name("core-api-gateway"))
	if err != nil {
		return nil, fmt.Errorf("connect nats: %w", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("jetstream: %w", err)
	}
	_, err = js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     IngestStream,
		Subjects: []string{"events.>"},
	})
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("ensure ingest stream: %w", err)
	}
	return &NATSPublisher{js: js}, nil
}

// Publish sends data with a "jetstream publish" span and injects the trace
// context into message headers so downstream consumers continue the trace.
func (p *NATSPublisher) Publish(ctx context.Context, subject string, data []byte) error {
	ctx, span := otel.Tracer("signalyard/core-api-gateway").Start(ctx, "jetstream publish "+subject)
	defer span.End()
	msg := &nats.Msg{Subject: subject, Data: data}
	platform.InjectNATS(ctx, msg)
	_, err := p.js.PublishMsg(ctx, msg)
	return err
}
