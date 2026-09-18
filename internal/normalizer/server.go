package normalizer

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"signalyard/internal/platform"
)

type Server struct {
	cfg     *Config
	store   *Store
	worker  *Worker
	auth    *platform.TokenAuth
	metrics *normalizerMetrics
}

type normalizerMetrics struct {
	registry *prometheus.Registry
	routed   *prometheus.CounterVec
}

func newNormalizerMetrics() *normalizerMetrics {
	reg := prometheus.NewRegistry()
	factory := promauto.With(reg)
	return &normalizerMetrics{
		registry: reg,
		routed: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: "signalyard",
			Subsystem: "normalizer",
			Name:      "events_processed_total",
			Help:      "Events processed by outcome.",
		}, []string{"category", "result"}),
	}
}

func NewServer(cfg *Config, store *Store, worker *Worker, auth *platform.TokenAuth) *Server {
	return &Server{cfg: cfg, store: store, worker: worker, auth: auth, metrics: newNormalizerMetrics()}
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(platform.CORS)

	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		platform.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Method(http.MethodGet, "/metrics", promhttp.HandlerFor(s.metrics.registry, promhttp.HandlerOpts{}))

	r.Group(func(r chi.Router) {
		r.Use(s.auth.Middleware)
		r.Get("/v1/quarantine", s.handleListQuarantine)
		r.Get("/v1/quarantine/{event_id}", s.handleGetQuarantine)
		r.Post("/v1/quarantine/{event_id}/replay", s.handleReplayOne)
		r.Post("/v1/replay/category/{category}", s.handleReplayCategory)
		r.Get("/v1/routing-stats", s.handleRoutingStats)
	})

	return r
}

func (s *Server) handleListQuarantine(w http.ResponseWriter, r *http.Request) {
	events, total, err := s.store.ListQuarantined(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		platform.WriteError(w, http.StatusInternalServerError, "list failed")
		return
	}
	platform.WriteJSON(w, http.StatusOK, map[string]any{"events": events, "total": total})
}

func (s *Server) handleGetQuarantine(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "event_id"))
	if err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid event_id")
		return
	}
	q, err := s.store.GetQuarantined(r.Context(), id)
	if err != nil {
		if err == ErrNotFound {
			platform.WriteError(w, http.StatusNotFound, "event not found")
			return
		}
		platform.WriteError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	platform.WriteJSON(w, http.StatusOK, map[string]any{
		"event_id":    q.EventID.String(),
		"raw_payload": q.RawPayload,
		"attempts":    q.Attempts,
		"received_at": q.ReceivedAt,
		"reason":      q.Reason,
		"status":      q.Status,
	})
}

func (s *Server) handleReplayOne(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "event_id"))
	if err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid event_id")
		return
	}
	q, err := s.store.GetQuarantined(r.Context(), id)
	if err != nil {
		if err == ErrNotFound {
			platform.WriteError(w, http.StatusNotFound, "event not found")
			return
		}
		platform.WriteError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	if q.Status != "pending" {
		platform.WriteError(w, http.StatusBadRequest, "event is not pending replay")
		return
	}
	ok, err := s.worker.ReplayOne(r.Context(), q)
	if err != nil {
		platform.WriteError(w, http.StatusInternalServerError, "replay failed")
		return
	}
	status := "still_failing"
	if ok {
		status = "replayed"
	}
	platform.WriteJSON(w, http.StatusOK, map[string]any{"event_id": id.String(), "status": status})
}

func (s *Server) handleReplayCategory(w http.ResponseWriter, r *http.Request) {
	category := chi.URLParam(r, "category")
	if _, err := s.worker.registry.GetSchema(r.Context(), category); err != nil {
		if err == ErrSchemaNotFound {
			platform.WriteError(w, http.StatusNotFound, "no schema for category")
			return
		}
		platform.WriteError(w, http.StatusBadGateway, "schema registry unreachable")
		return
	}
	n, err := s.worker.ReplayCategory(r.Context(), category)
	if err != nil {
		platform.WriteError(w, http.StatusInternalServerError, "replay failed")
		return
	}
	platform.WriteJSON(w, http.StatusOK, map[string]any{"category": category, "queued_count": n})
}

func (s *Server) handleRoutingStats(w http.ResponseWriter, _ *http.Request) {
	throughput, failRate, quarantineRate := s.worker.Stats()
	platform.WriteJSON(w, http.StatusOK, map[string]any{
		"throughput":              throughput,
		"validation_failure_rate": failRate,
		"quarantine_rate":         quarantineRate,
	})
}
