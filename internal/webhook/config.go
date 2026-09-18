package webhook

import (
	"fmt"
	"os"
	"strconv"
)

// Config for webhook_adapter_service. Names match docs/product.json.
type Config struct {
	Port                   int    // PORT (default 8084)
	NATSUrl                string // NATS_URL (required)
	PostgresDSN            string // POSTGRES_DSN (required: delivery tracking; spec's db_schema owns these tables)
	GitHubWebhookSecret    string // GITHUB_WEBHOOK_SECRET
	JiraWebhookSecret      string // JIRA_WEBHOOK_SECRET
	PagerDutyWebhookSecret string // PAGERDUTY_WEBHOOK_SECRET
	MarbleJarWebhookSecret string // MARBLE_JAR_WEBHOOK_SECRET
	HECTokenSalt           string // HEC_TOKEN_SALT (auth for the deliveries API)
	JWTSigningSecret       string // JWT_SIGNING_SECRET
	DevAdminToken          string // DEV_ADMIN_TOKEN (dev only)
}

func LoadConfig() (*Config, error) {
	cfg := &Config{
		Port:                   8084,
		NATSUrl:                os.Getenv("NATS_URL"),
		PostgresDSN:            os.Getenv("POSTGRES_DSN"),
		GitHubWebhookSecret:    os.Getenv("GITHUB_WEBHOOK_SECRET"),
		JiraWebhookSecret:      os.Getenv("JIRA_WEBHOOK_SECRET"),
		PagerDutyWebhookSecret: os.Getenv("PAGERDUTY_WEBHOOK_SECRET"),
		MarbleJarWebhookSecret: os.Getenv("MARBLE_JAR_WEBHOOK_SECRET"),
		HECTokenSalt:           os.Getenv("HEC_TOKEN_SALT"),
		JWTSigningSecret:       os.Getenv("JWT_SIGNING_SECRET"),
		DevAdminToken:          os.Getenv("DEV_ADMIN_TOKEN"),
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
		{"HEC_TOKEN_SALT", cfg.HECTokenSalt},
	} {
		if req.val == "" {
			return nil, fmt.Errorf("required environment variable %s is not set", req.name)
		}
	}
	return cfg, nil
}

func (c *Config) secretFor(tool string) string {
	switch tool {
	case "github":
		return c.GitHubWebhookSecret
	case "jira":
		return c.JiraWebhookSecret
	case "pagerduty":
		return c.PagerDutyWebhookSecret
	case "marble-jar":
		return c.MarbleJarWebhookSecret
	}
	return ""
}
