package coreapi

import (
	"context"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
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

func (p *NATSPublisher) Publish(ctx context.Context, subject string, data []byte) error {
	_, err := p.js.Publish(ctx, subject, data)
	return err
}
