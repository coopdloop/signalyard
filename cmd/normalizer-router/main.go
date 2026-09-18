package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"signalyard/internal/normalizer"
	"signalyard/internal/platform"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := normalizer.LoadConfig()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	telemetryShutdown, err := platform.SetupTelemetry(ctx, "normalizer_router")
	if err != nil {
		return fmt.Errorf("telemetry: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = telemetryShutdown(shutdownCtx)
	}()

	store, err := normalizer.NewStore(ctx, cfg.PostgresDSN)
	if err != nil {
		return err
	}
	defer store.Close()

	registryClient := normalizer.NewRegistryClient(cfg.SchemaRegistryURL, cfg.SchemaRegistryToken)
	worker, err := normalizer.NewWorker(cfg, store, registryClient, cfg.NATSUrl)
	if err != nil {
		return err
	}

	auth := platform.NewTokenAuth(store.Pool(), cfg.HECTokenSalt, cfg.JWTSigningSecret, cfg.DevAdminToken)
	srv := normalizer.NewServer(cfg, store, worker, auth)

	httpSrv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           otelhttp.NewHandler(srv.Router(), "normalizer_router"),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("normalizer_router listening", "port", cfg.Port)
		errCh <- httpSrv.ListenAndServe()
	}()
	go func() {
		if err := worker.Run(ctx); err != nil {
			errCh <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-stop:
		slog.Info("shutting down", "signal", sig.String())
		cancel()
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	return httpSrv.Shutdown(shutdownCtx)
}
