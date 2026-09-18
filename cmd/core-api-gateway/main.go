package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"signalyard/internal/coreapi"
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
	cfg, err := coreapi.LoadConfig()
	if err != nil {
		return err
	}
	ctx := context.Background()

	telemetryShutdown, err := platform.SetupTelemetry(ctx, "core_api_gateway")
	if err != nil {
		return fmt.Errorf("telemetry: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = telemetryShutdown(shutdownCtx)
	}()

	store, err := coreapi.NewStore(ctx, cfg.PostgresDSN)
	if err != nil {
		return err
	}
	defer store.Close()

	if cfg.RunMigrations {
		dir := os.Getenv("MIGRATIONS_DIR")
		if dir == "" {
			dir = "migrations"
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("read migrations dir: %w", err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
				continue
			}
			sql, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				return fmt.Errorf("read migration %s: %w", e.Name(), err)
			}
			if err := store.Migrate(ctx, e.Name(), string(sql)); err != nil {
				return fmt.Errorf("run migration %s: %w", e.Name(), err)
			}
			slog.Info("migration applied", "file", e.Name())
		}
	}

	pub, err := coreapi.NewNATSPublisher(ctx, cfg.NATSUrl)
	if err != nil {
		return err
	}

	srv := coreapi.NewServer(cfg, store, pub)
	httpSrv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           otelhttp.NewHandler(srv.Router(), "core_api_gateway"),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("core_api_gateway listening", "port", cfg.Port)
		errCh <- httpSrv.ListenAndServe()
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-stop:
		slog.Info("shutting down", "signal", sig.String())
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutdownCtx)
}
