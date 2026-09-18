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

	"signalyard/internal/coreapi"
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

	store, err := coreapi.NewStore(ctx, cfg.PostgresDSN)
	if err != nil {
		return err
	}
	defer store.Close()

	if cfg.RunMigrations {
		path := os.Getenv("MIGRATIONS_PATH")
		if path == "" {
			path = "migrations/0001_init.sql"
		}
		sql, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read migrations: %w", err)
		}
		if err := store.Migrate(ctx, string(sql)); err != nil {
			return fmt.Errorf("run migrations: %w", err)
		}
		slog.Info("migrations applied", "path", path)
	}

	pub, err := coreapi.NewNATSPublisher(ctx, cfg.NATSUrl)
	if err != nil {
		return err
	}

	srv := coreapi.NewServer(cfg, store, pub)
	httpSrv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           srv.Router(),
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
