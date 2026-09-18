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
	"signalyard/internal/webhook"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := webhook.LoadConfig()
	if err != nil {
		return err
	}
	ctx := context.Background()

	store, err := webhook.NewStore(ctx, cfg.PostgresDSN)
	if err != nil {
		return err
	}
	defer store.Close()

	if err := store.SeedSources(ctx, map[string]string{
		"github":     cfg.GitHubWebhookSecret,
		"jira":       cfg.JiraWebhookSecret,
		"pagerduty":  cfg.PagerDutyWebhookSecret,
		"marble-jar": cfg.MarbleJarWebhookSecret,
	}); err != nil {
		return fmt.Errorf("seed webhook sources: %w", err)
	}

	auth := platform.NewTokenAuth(store.Pool(), cfg.HECTokenSalt, cfg.JWTSigningSecret, cfg.DevAdminToken)
	srv, err := webhook.NewServer(ctx, cfg, store, auth)
	if err != nil {
		return err
	}

	httpSrv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("webhook_adapter_service listening", "port", cfg.Port)
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
