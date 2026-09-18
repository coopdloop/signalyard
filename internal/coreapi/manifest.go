package coreapi

// Manifest content served by /start-here-agents and /mcp/manifest.
// This is the self-onboarding contract every agent reads first.

func capabilityManifest() map[string]any {
	return map[string]any{
		"service":  "signal-yard core_api_gateway",
		"version":  "0.1.0",
		"protocols": []string{"rest-json", "otlp-http", "mcp"},
		"ingestion": map[string]any{
			"generic_events": map[string]any{
				"method": "POST",
				"path":   "/v1/collect",
				"auth":   "HEC-style token: Authorization: SignalYard <api_key>",
				"envelope": map[string]any{
					"required": []string{"category", "timestamp", "payload"},
					"optional": []string{"source"},
				},
			},
			"otlp": map[string]string{
				"traces":  "/v1/otlp/traces",
				"metrics": "/v1/otlp/metrics",
				"logs":    "/v1/otlp/logs",
			},
		},
		"unknown_shapes": "Events with unrecognized payload shapes are quarantined, classified by an AI agent, and proposed as schema patches for human review. Nothing is silently dropped.",
		"query": map[string]any{
			"mcp_tool": "query_events",
		},
	}
}

func mcpManifest() map[string]any {
	tool := func(name, description string, required []string, props map[string]string) map[string]any {
		properties := map[string]any{}
		for k, v := range props {
			properties[k] = map[string]string{"type": v}
		}
		return map[string]any{
			"name":        name,
			"description": description,
			"endpoint":    "/mcp/tools/" + name,
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": properties,
				"required":   required,
			},
		}
	}
	return map[string]any{
		"protocol": "mcp",
		"transport": map[string]any{
			"type":     "http",
			"basePath": "/mcp",
			"auth":     "HEC-style token: Authorization: SignalYard <api_key>",
		},
		"tools": []map[string]any{
			tool("ingest_event", "Ingest a single event into Signal Yard", []string{"category", "timestamp", "payload"}, map[string]string{
				"category": "string", "timestamp": "string", "payload": "object", "source": "string",
			}),
			tool("query_events", "Query stored events by category and time range", nil, map[string]string{
				"category": "string", "start_time": "string", "end_time": "string", "limit": "integer",
			}),
			tool("propose_schema", "Submit a schema proposal for human review", []string{"category", "json_schema"}, map[string]string{
				"category": "string", "json_schema": "object", "routing_yaml_diff": "string",
			}),
			tool("list_categories", "List known event categories", nil, nil),
			tool("get_manifest", "Return this capability/MCP manifest", nil, nil),
		},
	}
}

func examplePayloads() map[string]any {
	return map[string]any{
		"soar_alert": map[string]any{
			"category":  "soar_alert",
			"timestamp": "2026-09-18T19:00:00Z",
			"source":    "splunk-soar",
			"payload": map[string]any{
				"alert_id": "ALT-1234", "severity": "high", "rule": "suspicious-login",
			},
		},
		"dev_agent_trace": map[string]any{
			"category":  "dev_agent_trace",
			"timestamp": "2026-09-18T19:00:00Z",
			"source":    "my-coding-agent",
			"payload": map[string]any{
				"session_id": "sess-abc", "tool_calls": 7, "outcome": "success",
			},
		},
		"pm_ticket_update": map[string]any{
			"category":  "pm_ticket_update",
			"timestamp": "2026-09-18T19:00:00Z",
			"source":    "jira",
			"payload": map[string]any{
				"ticket": "PROJ-42", "status": "In Progress", "assignee": "ada",
			},
		},
	}
}

func apiKeyInstructions() string {
	return `1. POST /register with {"agent_name": "...", "contact_email": "..."} to register (no auth required).
2. A human approver approves your registration and issues an API key via POST /v1/api-keys.
3. Send events to POST /v1/collect with header "Authorization: SignalYard <api_key>".
4. Unknown payload shapes are quarantined and classified; a human reviews the proposed schema before it goes live.`
}
