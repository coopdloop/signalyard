package registry

import (
	"fmt"
	"os"
	"strconv"
)

// Config for schema_registry_service. Names match docs/product.json.
type Config struct {
	Port             int    // PORT (default 8082)
	PostgresDSN      string // POSTGRES_DSN (required)
	NATSUrl          string // NATS_URL (required)
	GitRepoURL       string // GIT_REPO_URL (spec-required; noop git when unset for dev)
	GitSSHKeyPath    string // GIT_SSH_KEY_PATH
	GitWorkDir       string // GIT_WORK_DIR (default: temp dir)
	HECTokenSalt     string // HEC_TOKEN_SALT (required: validates machine tokens)
	JWTSigningSecret string // JWT_SIGNING_SECRET (validates human session tokens)
	DevAdminToken    string // DEV_ADMIN_TOKEN (dev only)
}

func LoadConfig() (*Config, error) {
	cfg := &Config{
		Port:             8082,
		PostgresDSN:      os.Getenv("POSTGRES_DSN"),
		NATSUrl:          os.Getenv("NATS_URL"),
		GitRepoURL:       os.Getenv("GIT_REPO_URL"),
		GitSSHKeyPath:    os.Getenv("GIT_SSH_KEY_PATH"),
		GitWorkDir:       os.Getenv("GIT_WORK_DIR"),
		HECTokenSalt:     os.Getenv("HEC_TOKEN_SALT"),
		JWTSigningSecret: os.Getenv("JWT_SIGNING_SECRET"),
		DevAdminToken:    os.Getenv("DEV_ADMIN_TOKEN"),
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
		{"HEC_TOKEN_SALT", cfg.HECTokenSalt},
	} {
		if req.val == "" {
			return nil, fmt.Errorf("required environment variable %s is not set", req.name)
		}
	}
	return cfg, nil
}
