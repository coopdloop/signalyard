package normalizer

import (
	"fmt"
	"os"
	"strconv"
)

// Config for normalizer_router. Names match docs/product.json.
type Config struct {
	Port                int    // PORT (default 8081)
	NATSUrl             string // NATS_URL (required)
	PostgresDSN         string // POSTGRES_DSN (required)
	LokiPushURL         string // LOKI_PUSH_URL
	TempoOTLPEndpoint   string // TEMPO_OTLP_ENDPOINT (Phase 4: OTel Collector path)
	MimirRemoteWriteURL string // MIMIR_REMOTE_WRITE_URL (Phase 4)
	SchemaRegistryURL   string // SCHEMA_REGISTRY_URL (required)
	SchemaRegistryToken string // SCHEMA_REGISTRY_TOKEN (API key for registry calls)
	QuarantineStream    string // QUARANTINE_STREAM_NAME (default "quarantine")
	HECTokenSalt        string // HEC_TOKEN_SALT (auth for the HTTP API)
	JWTSigningSecret    string // JWT_SIGNING_SECRET
	DevAdminToken       string // DEV_ADMIN_TOKEN (dev only)
}

func LoadConfig() (*Config, error) {
	cfg := &Config{
		Port:                8081,
		NATSUrl:             os.Getenv("NATS_URL"),
		PostgresDSN:         os.Getenv("POSTGRES_DSN"),
		LokiPushURL:         os.Getenv("LOKI_PUSH_URL"),
		TempoOTLPEndpoint:   os.Getenv("TEMPO_OTLP_ENDPOINT"),
		MimirRemoteWriteURL: os.Getenv("MIMIR_REMOTE_WRITE_URL"),
		SchemaRegistryURL:   os.Getenv("SCHEMA_REGISTRY_URL"),
		SchemaRegistryToken: os.Getenv("SCHEMA_REGISTRY_TOKEN"),
		QuarantineStream:    envOr("QUARANTINE_STREAM_NAME", "quarantine"),
		HECTokenSalt:        os.Getenv("HEC_TOKEN_SALT"),
		JWTSigningSecret:    os.Getenv("JWT_SIGNING_SECRET"),
		DevAdminToken:       os.Getenv("DEV_ADMIN_TOKEN"),
	}
	if v := os.Getenv("PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("invalid PORT %q: %w", v, err)
		}
		cfg.Port = p
	}
	for _, req := range []struct{ name, val string }{
		{"NATS_URL", cfg.NATSUrl},
		{"POSTGRES_DSN", cfg.PostgresDSN},
		{"SCHEMA_REGISTRY_URL", cfg.SchemaRegistryURL},
		{"HEC_TOKEN_SALT", cfg.HECTokenSalt},
	} {
		if req.val == "" {
			return nil, fmt.Errorf("required environment variable %s is not set", req.name)
		}
	}
	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
