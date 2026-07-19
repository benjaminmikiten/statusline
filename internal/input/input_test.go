package input

import (
	"strings"
	"testing"
)

func TestParse_FullPayload(t *testing.T) {
	raw := `{
		"model": {"id": "claude-opus-4-8", "display_name": "Opus"},
		"workspace": {"current_dir": "/home/user/project", "project_dir": "/home/user/project"},
		"session_id": "abc123",
		"transcript_path": "/path/to/transcript.jsonl",
		"cost": {"total_cost_usd": 1.23, "total_duration_ms": 45000},
		"context_window": {
			"total_input_tokens": 15500,
			"total_output_tokens": 1200,
			"context_window_size": 200000,
			"used_percentage": 8.5,
			"remaining_percentage": 91.5,
			"current_usage": {
				"input_tokens": 8500,
				"output_tokens": 1200,
				"cache_creation_input_tokens": 5000,
				"cache_read_input_tokens": 2000
			}
		},
		"rate_limits": {
			"five_hour": {"used_percentage": 23.5, "resets_at": 1738425600},
			"seven_day": {"used_percentage": 41.2, "resets_at": 1738857600}
		}
	}`

	got, err := Parse(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if got.Model.DisplayName != "Opus" {
		t.Errorf("Model.DisplayName = %q, want %q", got.Model.DisplayName, "Opus")
	}
	if got.SessionID != "abc123" {
		t.Errorf("SessionID = %q, want %q", got.SessionID, "abc123")
	}
	if got.Cost.TotalCostUSD != 1.23 {
		t.Errorf("Cost.TotalCostUSD = %v, want 1.23", got.Cost.TotalCostUSD)
	}
	if got.ContextWindow == nil {
		t.Fatal("ContextWindow is nil")
	}
	if got.ContextWindow.UsedPercentage == nil || *got.ContextWindow.UsedPercentage != 8.5 {
		t.Errorf("ContextWindow.UsedPercentage = %v, want 8.5", got.ContextWindow.UsedPercentage)
	}
	if got.ContextWindow.CurrentUsage == nil || got.ContextWindow.CurrentUsage.CacheReadInputTokens != 2000 {
		t.Errorf("CurrentUsage.CacheReadInputTokens = %v, want 2000", got.ContextWindow.CurrentUsage)
	}
	if got.RateLimits == nil || got.RateLimits.FiveHour == nil || got.RateLimits.FiveHour.UsedPercentage != 23.5 {
		t.Errorf("RateLimits.FiveHour = %v, want used_percentage 23.5", got.RateLimits)
	}
}

func TestParse_MinimalPayload(t *testing.T) {
	// Fields the docs mark as null/absent before the first API response.
	raw := `{
		"model": {"id": "claude-opus-4-8", "display_name": "Opus"},
		"workspace": {"current_dir": "/home/user/project"},
		"session_id": "abc123"
	}`

	got, err := Parse(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if got.ContextWindow != nil {
		t.Errorf("ContextWindow = %+v, want nil", got.ContextWindow)
	}
	if got.RateLimits != nil {
		t.Errorf("RateLimits = %+v, want nil", got.RateLimits)
	}
}

func TestParse_InvalidJSON(t *testing.T) {
	_, err := Parse(strings.NewReader("not json"))
	if err == nil {
		t.Fatal("expected an error for invalid JSON, got nil")
	}
}
