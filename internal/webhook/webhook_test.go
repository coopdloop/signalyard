package webhook

import (
	"net/http/httptest"
	"testing"
)

func TestVerifyGitHubSignature(t *testing.T) {
	body := []byte(`{"action":"opened"}`)
	secret := "test-secret"
	r := httptest.NewRequest("POST", "/webhooks/github", nil)
	r.Header.Set("X-Hub-Signature-256", "sha256="+hmacSHA256(secret, body))
	if !verifySignature("github", r, body, secret) {
		t.Fatal("valid github signature rejected")
	}
	r.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	if verifySignature("github", r, body, secret) {
		t.Fatal("invalid github signature accepted")
	}
	if verifySignature("github", r, body, "") {
		t.Fatal("empty secret must fail closed")
	}
}

func TestVerifyPagerDutySignature(t *testing.T) {
	body := []byte(`{"event":{"id":"1"}}`)
	secret := "pd-secret"
	r := httptest.NewRequest("POST", "/webhooks/pagerduty", nil)
	r.Header.Set("X-PagerDuty-Signature", "v1=deadbeef,v1="+hmacSHA256(secret, body))
	if !verifySignature("pagerduty", r, body, secret) {
		t.Fatal("valid pagerduty signature rejected")
	}
}

func TestVerifyGenericSignature(t *testing.T) {
	body := []byte(`{"k":"v"}`)
	secret := "mj-secret"
	r := httptest.NewRequest("POST", "/webhooks/marble-jar", nil)
	r.Header.Set("X-SignalYard-Signature", "sha256="+hmacSHA256(secret, body))
	if !verifySignature("marble-jar", r, body, secret) {
		t.Fatal("valid marble-jar signature rejected")
	}
	if verifySignature("jira", r, body, "other-secret") {
		t.Fatal("wrong secret accepted")
	}
}

func TestNormalize(t *testing.T) {
	r := httptest.NewRequest("POST", "/webhooks/github", nil)
	r.Header.Set("X-GitHub-Event", "pull_request")
	env := normalize("github", r, map[string]any{"action": "opened"})
	if env.Category != "github_event" || env.Payload["event"] != "pull_request" || env.Payload["action"] != "opened" {
		t.Fatalf("bad github envelope: %+v", env)
	}

	env = normalize("jira", r, map[string]any{"webhookEvent": "jira:issue_updated"})
	if env.Category != "pm_ticket_update" || env.Payload["webhook_event"] != "jira:issue_updated" {
		t.Fatalf("bad jira envelope: %+v", env)
	}

	env = normalize("pagerduty", r, map[string]any{"event": map[string]any{"id": "1"}})
	if env.Category != "soar_alert" {
		t.Fatalf("bad pagerduty envelope: %+v", env)
	}

	env = normalize("marble-jar", r, map[string]any{"marbles": 3})
	if env.Category != "marble_jar_event" || env.Payload["marbles"] != 3 {
		t.Fatalf("bad marble-jar envelope: %+v", env)
	}
}
