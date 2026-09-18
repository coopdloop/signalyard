package normalizer

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestValidatePayload(t *testing.T) {
	schema := map[string]any{
		"type":     "object",
		"required": []any{"alert_id", "severity"},
		"properties": map[string]any{
			"alert_id": map[string]any{"type": "string"},
			"severity": map[string]any{"type": "string", "enum": []any{"low", "medium", "high"}},
		},
	}

	ok, err := validatePayload(schema, map[string]any{"alert_id": "A1", "severity": "high"})
	if err != nil || !ok.valid {
		t.Fatalf("expected valid, got %+v err=%v", ok, err)
	}

	missing, err := validatePayload(schema, map[string]any{"severity": "high"})
	if err != nil {
		t.Fatal(err)
	}
	if missing.valid {
		t.Fatal("expected invalid for missing required field")
	}

	badEnum, err := validatePayload(schema, map[string]any{"alert_id": "A1", "severity": "critical"})
	if err != nil {
		t.Fatal(err)
	}
	if badEnum.valid {
		t.Fatal("expected invalid for enum violation")
	}
}

func TestRoutingConfigDestinations(t *testing.T) {
	cases := []struct {
		yaml string
		want []string
	}{
		{"target: postgres", []string{"postgres"}},
		{"targets: [postgres, loki]", []string{"postgres", "loki"}},
		{"", []string{"postgres"}},
	}
	for _, tc := range cases {
		var rc routingConfig
		if err := yaml.Unmarshal([]byte(tc.yaml), &rc); err != nil {
			t.Fatal(err)
		}
		got := rc.destinations()
		if len(got) != len(tc.want) {
			t.Fatalf("yaml %q: got %v want %v", tc.yaml, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("yaml %q: got %v want %v", tc.yaml, got, tc.want)
			}
		}
	}
}
