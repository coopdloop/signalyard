package normalizer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/xeipuuv/gojsonschema"
	"gopkg.in/yaml.v3"
)

// routingConfig is the per-category routing YAML contract:
//
//	target: postgres          # single destination
//	targets: [postgres, loki] # or fan-out
type routingConfig struct {
	Target  string   `yaml:"target"`
	Targets []string `yaml:"targets"`
}

func (r routingConfig) destinations() []string {
	if len(r.Targets) > 0 {
		return r.Targets
	}
	if r.Target != "" {
		return []string{r.Target}
	}
	return []string{"postgres"}
}

type counters struct {
	processed   atomic.Int64
	routed      atomic.Int64
	failedValid atomic.Int64
	quarantined atomic.Int64
	replayed    atomic.Int64
	startedAt   time.Time
}

type Worker struct {
	cfg      *Config
	store    *Store
	registry *RegistryClient
	nc       *nats.Conn
	js       jetstream.JetStream
	counts   *counters
}

func NewWorker(cfg *Config, store *Store, registry *RegistryClient, natsURL string) (*Worker, error) {
	nc, err := nats.Connect(natsURL, nats.Timeout(10*time.Second), nats.Name("normalizer-router"))
	if err != nil {
		return nil, fmt.Errorf("connect nats: %w", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, err
	}
	return &Worker{cfg: cfg, store: store, registry: registry, nc: nc, js: js, counts: &counters{startedAt: time.Now()}}, nil
}

// Run starts the ingestion consumer and the schema-approval listener until ctx ends.
func (w *Worker) Run(ctx context.Context) error {
	if _, err := w.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     w.cfg.QuarantineStream,
		Subjects: []string{"quarantine.>"},
	}); err != nil {
		return fmt.Errorf("ensure quarantine stream: %w", err)
	}

	ingest, err := w.js.CreateOrUpdateConsumer(ctx, "ingest", jetstream.ConsumerConfig{
		Durable:       "normalizer",
		FilterSubject: "events.ingest.>",
		AckPolicy:     jetstream.AckExplicitPolicy,
	})
	if err != nil {
		return fmt.Errorf("ingest consumer: %w", err)
	}
	approvals, err := w.js.CreateOrUpdateConsumer(ctx, "schema_events", jetstream.ConsumerConfig{
		Durable:       "normalizer-approvals",
		FilterSubject: "schema.approved",
		AckPolicy:     jetstream.AckExplicitPolicy,
	})
	if err != nil {
		return fmt.Errorf("approvals consumer: %w", err)
	}

	ingestCtx, err := ingest.Consume(func(msg jetstream.Msg) {
		if err := w.handleIngest(ctx, msg); err != nil {
			slog.Error("ingest handling failed", "error", err)
		}
	})
	if err != nil {
		return err
	}
	approvalCtx, err := approvals.Consume(func(msg jetstream.Msg) {
		if err := w.handleApproval(ctx, msg); err != nil {
			slog.Error("approval handling failed", "error", err)
		}
	})
	if err != nil {
		return err
	}

	slog.Info("normalizer_router consuming", "stream", "ingest", "quarantine_stream", w.cfg.QuarantineStream)
	<-ctx.Done()
	ingestCtx.Stop()
	approvalCtx.Stop()
	w.nc.Drain()
	return nil
}

func (w *Worker) handleIngest(ctx context.Context, msg jetstream.Msg) error {
	var env Envelope
	if err := json.Unmarshal(msg.Data(), &env); err != nil {
		// Unparseable: dead-letter by quarantining the raw bytes, then ack.
		slog.Warn("unparseable envelope, dropping to quarantine", "subject", msg.Subject())
		w.counts.quarantined.Add(1)
		return msg.Ack()
	}
	if _, err := w.Process(ctx, env); err != nil {
		slog.Error("process failed", "category", env.Category, "error", err)
		// Nak so transient failures (e.g. postgres down) are redelivered.
		return msg.NakWithDelay(5 * time.Second)
	}
	return msg.Ack()
}

func (w *Worker) handleApproval(ctx context.Context, msg jetstream.Msg) error {
	var ev struct {
		Category string `json:"category"`
	}
	if err := json.Unmarshal(msg.Data(), &ev); err != nil || ev.Category == "" {
		return msg.Ack()
	}
	w.registry.Invalidate(ev.Category)
	n, err := w.ReplayCategory(ctx, ev.Category)
	if err != nil {
		slog.Error("replay after approval failed", "category", ev.Category, "error", err)
		return msg.NakWithDelay(10 * time.Second)
	}
	slog.Info("replayed quarantined events after approval", "category", ev.Category, "replayed", n)
	return msg.Ack()
}

// Process validates one envelope against the registry schema and routes it.
// Returns ("routed"|"quarantined", err). Processing errors are transient
// infrastructure failures; validation outcomes are not errors.
func (w *Worker) Process(ctx context.Context, env Envelope) (string, error) {
	w.counts.processed.Add(1)

	schema, err := w.registry.GetSchema(ctx, env.Category)
	if errors.Is(err, ErrSchemaNotFound) {
		return "quarantined", w.quarantine(ctx, env, "unknown_category")
	}
	if err != nil {
		return "", fmt.Errorf("schema lookup: %w", err)
	}

	valid, err := validatePayload(schema.JSONSchema, env.Payload)
	if err != nil {
		return "", fmt.Errorf("validate: %w", err)
	}
	if !valid.valid {
		w.counts.failedValid.Add(1)
		return "quarantined", w.quarantine(ctx, env, "schema_validation_failed: "+valid.reason)
	}

	var rc routingConfig
	if schema.RoutingYAML != "" {
		if err := yaml.Unmarshal([]byte(schema.RoutingYAML), &rc); err != nil {
			slog.Warn("unparseable routing yaml, defaulting to postgres", "category", env.Category, "error", err)
		}
	}
	for _, dest := range rc.destinations() {
		if err := w.route(ctx, dest, env); err != nil {
			return "", fmt.Errorf("route to %s: %w", dest, err)
		}
	}
	w.counts.routed.Add(1)
	return "routed", nil
}

type validationResult struct {
	valid  bool
	reason string
}

func validatePayload(jsonSchema map[string]any, payload map[string]any) (validationResult, error) {
	result, err := gojsonschema.Validate(gojsonschema.NewGoLoader(jsonSchema), gojsonschema.NewGoLoader(payload))
	if err != nil {
		return validationResult{}, err
	}
	if result.Valid() {
		return validationResult{valid: true}, nil
	}
	reason := "payload does not match schema"
	if len(result.Errors()) > 0 {
		reason = result.Errors()[0].String()
	}
	return validationResult{valid: false, reason: reason}, nil
}

func (w *Worker) route(ctx context.Context, dest string, env Envelope) error {
	switch dest {
	case "postgres":
		return w.store.InsertEvent(ctx, env)
	case "loki":
		return w.pushLoki(ctx, env)
	case "tempo", "mimir":
		// OTLP-native forwarding lands with the OTel Collector in Phase 4;
		// events are durable in the ingest stream until then.
		slog.Debug("destination deferred to Phase 4 collector path", "dest", dest, "category", env.Category)
		return nil
	default:
		slog.Warn("unknown routing target, using postgres", "target", dest, "category", env.Category)
		return w.store.InsertEvent(ctx, env)
	}
}

func (w *Worker) pushLoki(ctx context.Context, env Envelope) error {
	if w.cfg.LokiPushURL == "" {
		slog.Debug("LOKI_PUSH_URL not set, skipping loki route", "category", env.Category)
		return nil
	}
	line, _ := json.Marshal(env.Payload)
	ts := env.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}
	body, _ := json.Marshal(map[string]any{
		"streams": []map[string]any{{
			"stream": map[string]string{"category": env.Category, "source": env.Source},
			"values": [][]string{{fmt.Sprintf("%d", ts.UnixNano()), string(line)}},
		}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.cfg.LokiPushURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("loki push returned %d: %s", resp.StatusCode, msg)
	}
	return nil
}

func (w *Worker) quarantine(ctx context.Context, env Envelope, reason string) error {
	if err := w.store.Quarantine(ctx, env, reason); err != nil {
		return fmt.Errorf("record quarantine: %w", err)
	}
	data, _ := json.Marshal(env)
	if _, err := w.js.Publish(ctx, "quarantine.unknown", data); err != nil {
		return fmt.Errorf("publish quarantine: %w", err)
	}
	w.counts.quarantined.Add(1)
	return nil
}

// ReplayCategory reprocesses pending quarantined events for a category.
// Returns the number successfully replayed.
func (w *Worker) ReplayCategory(ctx context.Context, category string) (int, error) {
	events, err := w.store.QuarantinedByCategory(ctx, category)
	if err != nil {
		return 0, err
	}
	replayed := 0
	for _, q := range events {
		ok, err := w.ReplayOne(ctx, q)
		if err != nil {
			return replayed, err
		}
		if ok {
			replayed++
		}
	}
	return replayed, nil
}

// ReplayOne reprocesses a single quarantined event; true if it now routes.
func (w *Worker) ReplayOne(ctx context.Context, q QuarantineEvent) (bool, error) {
	raw, _ := json.Marshal(q.RawPayload)
	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return false, w.store.BumpAttempts(ctx, q.EventID, "unparseable stored payload")
	}
	result, err := w.Process(ctx, env)
	if err != nil {
		return false, err
	}
	if result == "routed" {
		w.counts.replayed.Add(1)
		return true, w.store.MarkReplayed(ctx, q.EventID)
	}
	return false, w.store.BumpAttempts(ctx, q.EventID, "replay: still failing validation")
}

func (w *Worker) Stats() (throughput, validationFailureRate, quarantineRate float64) {
	processed := float64(w.counts.processed.Load())
	uptime := time.Since(w.counts.startedAt).Seconds()
	if uptime > 0 {
		throughput = processed / uptime
	}
	if processed > 0 {
		validationFailureRate = float64(w.counts.failedValid.Load()) / processed
		quarantineRate = float64(w.counts.quarantined.Load()) / processed
	}
	return throughput, validationFailureRate, quarantineRate
}
