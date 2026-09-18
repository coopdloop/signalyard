package coreapi

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type metrics struct {
	registry       *prometheus.Registry
	ingestedEvents *prometheus.CounterVec
	httpRequests   *prometheus.CounterVec
}

func newMetrics() *metrics {
	reg := prometheus.NewRegistry()
	factory := promauto.With(reg)
	return &metrics{
		registry: reg,
		ingestedEvents: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: "signalyard",
			Subsystem: "gateway",
			Name:      "ingested_events_total",
			Help:      "Events accepted and published to the ingestion stream.",
		}, []string{"category", "kind"}),
		httpRequests: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: "signalyard",
			Subsystem: "gateway",
			Name:      "http_requests_total",
			Help:      "HTTP requests by route, method and status.",
		}, []string{"route", "method", "status"}),
	}
}

func (m *metrics) handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}
