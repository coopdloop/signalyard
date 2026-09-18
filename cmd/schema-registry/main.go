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

	"signalyard/internal/platform"
	"signalyard/internal/registry"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := registry.LoadConfig()
	if err != nil {
		return err
	}
	ctx := context.Background()

	store, err := registry.NewStore(ctx, cfg.PostgresDSN)
	if err != nil {
		return err
	}
	defer store.Close()

	gitBackend, err := registry.NewGitCommitter(ctx, cfg)
	if err != nil {
		return err
	}

	events, err := registry.NewNATSEventPublisher(ctx, cfg.NATSUrl)
	if err != nil {
		return err
	}

	auth := platform.NewTokenAuth(store.Pool(), cfg.HECTokenSalt, cfg.JWTSigningSecret, cfg.DevAdminToken)
	srv := registry.NewServer(cfg, store, gitBackend, events, auth)

	httpSrv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("schema_registry_service listening", "port", cfg.Port)
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
