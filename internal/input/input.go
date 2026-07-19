// Package input defines the JSON schema Claude Code sends to a statusLine
// command on stdin, and parses it.
package input

import (
	"encoding/json"
	"fmt"
	"io"
)

type Input struct {
	Model          Model          `json:"model"`
	Workspace      Workspace      `json:"workspace"`
	SessionID      string         `json:"session_id"`
	TranscriptPath string         `json:"transcript_path"`
	Cost           Cost           `json:"cost"`
	ContextWindow  *ContextWindow `json:"context_window"`
	RateLimits     *RateLimits    `json:"rate_limits"`
}

type Model struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

type Workspace struct {
	CurrentDir string `json:"current_dir"`
	ProjectDir string `json:"project_dir"`
}

type Cost struct {
	TotalCostUSD     float64 `json:"total_cost_usd"`
	TotalDurationMs  int64   `json:"total_duration_ms"`
	TotalAPIDuration int64   `json:"total_api_duration_ms"`
}

type ContextWindow struct {
	TotalInputTokens    int64         `json:"total_input_tokens"`
	TotalOutputTokens   int64         `json:"total_output_tokens"`
	ContextWindowSize   int64         `json:"context_window_size"`
	UsedPercentage      *float64      `json:"used_percentage"`
	RemainingPercentage *float64      `json:"remaining_percentage"`
	CurrentUsage        *CurrentUsage `json:"current_usage"`
}

type CurrentUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
}

type RateLimits struct {
	FiveHour *RateWindow `json:"five_hour"`
	SevenDay *RateWindow `json:"seven_day"`
}

type RateWindow struct {
	UsedPercentage float64 `json:"used_percentage"`
	ResetsAt       int64   `json:"resets_at"`
}

// Parse decodes the statusline JSON payload from r.
func Parse(r io.Reader) (*Input, error) {
	var in Input
	dec := json.NewDecoder(r)
	if err := dec.Decode(&in); err != nil {
		return nil, fmt.Errorf("input: decode: %w", err)
	}
	return &in, nil
}
