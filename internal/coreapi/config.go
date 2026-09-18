package coreapi

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds all environment-driven settings for core_api_gateway.
// Names match the product spec (docs/product.json).
type Config struct {
	Port                  int     // PORT (default 8080)
	PostgresDSN           string  // POSTGRES_DSN (required)
	NATSUrl               string  // NATS_URL (required)
	OIDCIssuerURL         string  // OIDC_ISSUER_URL (required by spec; /login returns 503 if unset)
	OIDCClientID          string  // OIDC_CLIENT_ID
	OIDCClientSecret      string  // OIDC_CLIENT_SECRET
	JWTSigningSecret      string  // JWT_SIGNING_SECRET (required)
	HECTokenSalt          string  // HEC_TOKEN_SALT (required)
	OTELCollectorEndpoint string  // OTEL_COLLECTOR_ENDPOINT
	SchemaRegistryURL     string  // SCHEMA_REGISTRY_URL (dependency arrives in Phase 2)
	DevAdminToken         string  // DEV_ADMIN_TOKEN (optional, local dev only)
	RunMigrations         bool    // RUN_MIGRATIONS (default false)
	RateLimitRPS          float64 // RATE_LIMIT_RPS (default 50)
	RateLimitBurst        float64 // RATE_LIMIT_BURST (default 100)
}

func LoadConfig() (*Config, error) {
	cfg := &Config{
		Port:                  8080,
		PostgresDSN:           os.Getenv("POSTGRES_DSN"),
		NATSUrl:               os.Getenv("NATS_URL"),
		OIDCIssuerURL:         os.Getenv("OIDC_ISSUER_URL"),
		OIDCClientID:          os.Getenv("OIDC_CLIENT_ID"),
		OIDCClientSecret:      os.Getenv("OIDC_CLIENT_SECRET"),
		JWTSigningSecret:      os.Getenv("JWT_SIGNING_SECRET"),
		HECTokenSalt:          os.Getenv("HEC_TOKEN_SALT"),
		OTELCollectorEndpoint: os.Getenv("OTEL_COLLECTOR_ENDPOINT"),
		SchemaRegistryURL:     os.Getenv("SCHEMA_REGISTRY_URL"),
		DevAdminToken:         os.Getenv("DEV_ADMIN_TOKEN"),
		RunMigrations:         os.Getenv("RUN_MIGRATIONS") == "true",
		RateLimitRPS:          50,
		RateLimitBurst:        100,
	}
	if v := os.Getenv("RATE_LIMIT_RPS"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid RATE_LIMIT_RPS %q: %w", v, err)
		}
		cfg.RateLimitRPS = f
	}
	if v := os.Getenv("RATE_LIMIT_BURST"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid RATE_LIMIT_BURST %q: %w", v, err)
		}
		cfg.RateLimitBurst = f
	}
	if v := os.Getenv("PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("invalid PORT %q: %w", v, err)
		}
		cfg.Port = p
	}
	for _, req := range []struct{ name, val string }{
		{"POSTGRES_DSN", cfg.PostgresDSN},
		{"NATS_URL", cfg.NATSUrl},
		{"JWT_SIGNING_SECRET", cfg.JWTSigningSecret},
		{"HEC_TOKEN_SALT", cfg.HECTokenSalt},
	} {
		if req.val == "" {
			return nil, fmt.Errorf("required environment variable %s is not set", req.name)
		}
	}
	return cfg, nil
}
