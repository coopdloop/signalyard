package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"

	"signalyard/internal/platform"
)

var tools = []string{"github", "jira", "pagerduty", "marble-jar"}

type Server struct {
	cfg     *Config
	store   *Store
	js      jetstream.JetStream
	auth    *platform.TokenAuth
	metrics *webhookMetrics
}

type webhookMetrics struct {
	registry   *prometheus.Registry
	deliveries *prometheus.CounterVec
}

func newWebhookMetrics() *webhookMetrics {
	reg := prometheus.NewRegistry()
	factory := promauto.With(reg)
	return &webhookMetrics{
		registry: reg,
		deliveries: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: "signalyard",
			Subsystem: "webhook_adapter",
			Name:      "deliveries_total",
			Help:      "Webhook deliveries by tool and outcome.",
		}, []string{"tool", "outcome"}),
	}
}

func NewServer(ctx context.Context, cfg *Config, store *Store, auth *platform.TokenAuth) (*Server, error) {
	nc, err := nats.Connect(cfg.NATSUrl, nats.Timeout(10*time.Second), nats.Name("webhook-adapter"))
	if err != nil {
		return nil, fmt.Errorf("connect nats: %w", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, err
	}
	// Dead-letter stream for forward failures.
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     "webhooks",
		Subjects: []string{"webhooks.>"},
	}); err != nil {
		nc.Close()
		return nil, fmt.Errorf("ensure webhooks stream: %w", err)
	}
	return &Server{cfg: cfg, store: store, js: js, auth: auth, metrics: newWebhookMetrics()}, nil
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)

	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		platform.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Method(http.MethodGet, "/metrics", promhttp.HandlerFor(s.metrics.registry, promhttp.HandlerOpts{}))

	for _, tool := range tools {
		r.Post("/webhooks/"+tool, s.handleWebhook(tool))
	}

	r.Group(func(r chi.Router) {
		r.Use(s.auth.Middleware)
		r.Get("/v1/webhook-deliveries", s.handleListDeliveries)
		r.Get("/v1/webhook-deliveries/{delivery_id}", s.handleGetDelivery)
		r.Post("/v1/webhook-deliveries/{delivery_id}/retry", s.handleRetryDelivery)
	})

	return r
}

func (s *Server) handleWebhook(tool string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
		if err != nil || len(body) == 0 {
			platform.WriteError(w, http.StatusBadRequest, "empty or unreadable payload")
			return
		}
		if !verifySignature(tool, r, body, s.cfg.secretFor(tool)) {
			s.metrics.deliveries.WithLabelValues(tool, "bad_signature").Inc()
			platform.WriteError(w, http.StatusUnauthorized, "invalid webhook signature")
			return
		}
		var raw map[string]any
		if err := json.Unmarshal(body, &raw); err != nil {
			platform.WriteError(w, http.StatusBadRequest, "payload must be a JSON object")
			return
		}

		env := normalize(tool, r, raw)
		env.EventID = uuid.NewString()
		normalized, err := env.bytes()
		if err != nil {
			platform.WriteError(w, http.StatusBadRequest, "failed to normalize payload")
			return
		}

		headers := map[string]string{}
		for _, h := range []string{"X-GitHub-Event", "X-GitHub-Delivery", "User-Agent", "Content-Type"} {
			if v := r.Header.Get(h); v != "" {
				headers[h] = v
			}
		}
		deliveryID, err := s.store.RecordDelivery(r.Context(), tool, true, raw, normalized, headers)
		if err != nil {
			platform.WriteError(w, http.StatusInternalServerError, "failed to record delivery")
			return
		}

		if err := s.forward(r.Context(), env.Category, normalized); err != nil {
			_ = s.store.MarkFailed(r.Context(), deliveryID, err.Error())
			s.deadLetter(r.Context(), tool, deliveryID, normalized, err)
			s.metrics.deliveries.WithLabelValues(tool, "forward_failed").Inc()
			platform.WriteError(w, http.StatusBadGateway, "failed to forward event")
			return
		}
		_ = s.store.MarkForwarded(r.Context(), deliveryID)
		s.metrics.deliveries.WithLabelValues(tool, "forwarded").Inc()
		platform.WriteJSON(w, http.StatusAccepted, map[string]string{
			"event_id": env.EventID, "status": "accepted",
		})
	}
}

func (s *Server) forward(ctx context.Context, category string, normalized []byte) error {
	subject := "events.ingest." + category
	ctx, span := otel.Tracer("signalyard/webhook-adapter").Start(ctx, "jetstream publish "+subject)
	defer span.End()
	msg := &nats.Msg{Subject: subject, Data: normalized}
	platform.InjectNATS(ctx, msg)
	_, err := s.js.PublishMsg(ctx, msg)
	return err
}

func (s *Server) deadLetter(ctx context.Context, tool string, deliveryID uuid.UUID, normalized []byte, fwdErr error) {
	msg, _ := json.Marshal(map[string]any{
		"tool": tool, "delivery_id": deliveryID.String(), "error": fwdErr.Error(),
		"normalized": json.RawMessage(normalized), "failed_at": time.Now().UTC(),
	})
	_, _ = s.js.Publish(ctx, "webhooks.deadletter", msg)
}

func (s *Server) handleListDeliveries(w http.ResponseWriter, r *http.Request) {
	deliveries, err := s.store.ListDeliveries(r.Context(), 100)
	if err != nil {
		platform.WriteError(w, http.StatusInternalServerError, "list failed")
		return
	}
	platform.WriteJSON(w, http.StatusOK, map[string]any{"deliveries": deliveries})
}

func (s *Server) handleGetDelivery(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "delivery_id"))
	if err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid delivery_id")
		return
	}
	d, err := s.store.GetDelivery(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			platform.WriteError(w, http.StatusNotFound, "delivery not found")
			return
		}
		platform.WriteError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	platform.WriteJSON(w, http.StatusOK, map[string]any{
		"delivery_id":      d.ID.String(),
		"tool":             d.Tool,
		"status":           d.Status,
		"raw_payload":      d.RawPayload,
		"normalized_event": d.Normalized,
		"error":            d.Error,
	})
}

func (s *Server) handleRetryDelivery(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "delivery_id"))
	if err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid delivery_id")
		return
	}
	d, err := s.store.GetDelivery(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			platform.WriteError(w, http.StatusNotFound, "delivery not found")
			return
		}
		platform.WriteError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	if d.Status == "forwarded" {
		platform.WriteError(w, http.StatusBadRequest, "delivery already forwarded")
		return
	}
	var env Envelope
	if err := json.Unmarshal(d.Normalized, &env); err != nil {
		platform.WriteError(w, http.StatusBadRequest, "stored delivery has no normalized event")
		return
	}
	if err := s.forward(r.Context(), env.Category, d.Normalized); err != nil {
		_ = s.store.MarkFailed(r.Context(), id, err.Error())
		platform.WriteError(w, http.StatusBadGateway, "retry forward failed")
		return
	}
	_ = s.store.MarkForwarded(r.Context(), id)
	platform.WriteJSON(w, http.StatusOK, map[string]string{"delivery_id": id.String(), "status": "forwarded"})
}
