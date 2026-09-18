package platform

import (
	"context"
	"testing"

	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func TestSetupTelemetryNoopWithoutEndpoint(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	shutdown, err := SetupTelemetry(context.Background(), "test-service")
	if err != nil {
		t.Fatal(err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestNATSContextRoundTrip(t *testing.T) {
	otel.SetTextMapPropagator(propagation.TraceContext{})
	tp := sdktrace.NewTracerProvider()
	otel.SetTracerProvider(tp)

	tracer := tp.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "parent")
	defer span.End()
	wantTraceID := span.SpanContext().TraceID()

	msg := &nats.Msg{Subject: "events.ingest.test", Data: []byte(`{}`)}
	InjectNATS(ctx, msg)

	if msg.Header.Get("traceparent") == "" {
		t.Fatal("traceparent header not injected")
	}

	extracted := ExtractNATS(context.Background(), msg.Header)
	sc := trace.SpanContextFromContext(extracted)
	if sc.TraceID() != wantTraceID {
		t.Fatalf("trace id mismatch: got %s want %s", sc.TraceID(), wantTraceID)
	}
}

func TestExtractNATSNilHeaders(t *testing.T) {
	ctx := context.Background()
	if got := ExtractNATS(ctx, nil); got != ctx {
		t.Fatal("nil headers must return the original context")
	}
}
