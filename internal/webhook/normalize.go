package webhook

import (
	"encoding/json"
	"net/http"
	"time"
)

// Envelope matches the gateway's ingest contract (category/timestamp/payload/source).
type Envelope struct {
	EventID   string         `json:"event_id"`
	AgentID   string         `json:"agent_id,omitempty"`
	AgentSlug string         `json:"agent_slug,omitempty"`
	Category  string         `json:"category"`
	Source    string         `json:"source"`
	Timestamp time.Time      `json:"timestamp"`
	Payload   map[string]any `json:"payload"`
}

// normalize maps a tool's native payload into the common envelope.
// Categories align with the spec's example payloads where applicable.
func normalize(tool string, r *http.Request, raw map[string]any) Envelope {
	now := time.Now().UTC()
	env := Envelope{Source: tool, Timestamp: now, Payload: raw}
	switch tool {
	case "github":
		env.Category = "github_event"
		env.Payload = map[string]any{
			"event":      r.Header.Get("X-GitHub-Event"),
			"delivery":   r.Header.Get("X-GitHub-Delivery"),
			"action":     raw["action"],
			"repository": raw["repository"],
			"sender":     raw["sender"],
		}
	case "jira":
		env.Category = "pm_ticket_update"
		env.Payload = map[string]any{
			"webhook_event": raw["webhookEvent"],
			"issue":         raw["issue"],
			"user":          raw["user"],
			"changelog":     raw["changelog"],
		}
	case "pagerduty":
		env.Category = "soar_alert"
		env.Payload = map[string]any{
			"event":    raw["event"],
			"messages": raw["messages"],
		}
	default: // marble-jar
		env.Category = "marble_jar_event"
		env.Payload = raw
	}
	return env
}

func (e Envelope) bytes() ([]byte, error) {
	return json.Marshal(e)
}
