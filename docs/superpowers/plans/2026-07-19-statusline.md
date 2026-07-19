# Cost-Aware Go Statusline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Go binary that Claude Code invokes as its `statusLine.command`, showing model/git/cost/context on two fixed lines and a conditional third line that warns when the prompt cache is about to expire, has gone cold, or context is nearing the compact threshold.

**Architecture:** Single-binary CLI, no daemon. `main.go` reads the documented statusline JSON from stdin, fans out to small pure-logic and I/O packages under `internal/`, and prints 2–3 text lines to stdout. Design doc: `docs/superpowers/specs/2026-07-19-statusline-design.md`.

**Tech Stack:** Go (standard library only — `encoding/json`, `os/exec`, `time`, `bufio`). No third-party dependencies.

## Global Constraints

- Module name: `statusline`. Go 1.22+ (from `go.mod`).
- No third-party dependencies — standard library only, per the design doc's "no new dependencies" decision for config parsing.
- The binary must always exit 0 and always write something to stdout, even in a degraded state (missing fields, unreadable transcript, no git repo) — a non-zero exit or empty stdout blanks the entire Claude Code statusline per Claude Code's own documented behavior.
- All subprocess calls and file reads that touch the filesystem/git must use a hard timeout (~200ms) via `context.Context`.
- Default compact threshold: 80 (percent). Default cache TTL when undetectable: 5 minutes.
- Cache countdown states: `warm` (>60s remaining), `warning` (≤60s), `critical` (≤15s), `cold` (TTL elapsed).

---

### Task 1: Module setup + `internal/input` (parse statusline JSON)

**Files:**
- Create: `go.mod`
- Create: `internal/input/input.go`
- Test: `internal/input/input_test.go`

**Interfaces:**
- Produces: `input.Parse(r io.Reader) (*input.Input, error)`, and the `input.Input`, `input.ContextWindow`, `input.CurrentUsage`, `input.RateLimits`, `input.RateWindow` structs (field names/types below), consumed by every later task.

- [ ] **Step 1: Initialize the Go module**

Run:
```bash
cd /Users/benjaminmikiten/projects/statusline
go mod init statusline
```
Expected: creates `go.mod` with `module statusline` and a `go` directive.

- [ ] **Step 2: Write the failing test**

Create `internal/input/input_test.go`:
```go
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
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/input/... -v`
Expected: FAIL — `input` package / `Parse` undefined.

- [ ] **Step 4: Write the implementation**

Create `internal/input/input.go`:
```go
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
	TotalCostUSD      float64 `json:"total_cost_usd"`
	TotalDurationMs   int64   `json:"total_duration_ms"`
	TotalAPIDuration  int64   `json:"total_api_duration_ms"`
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
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/input/... -v`
Expected: PASS (all three tests).

- [ ] **Step 6: Commit**

```bash
git add go.mod internal/input
git commit -m "feat: add statusline JSON input parsing"
```

---

### Task 2: `internal/cache` (cache-TTL state logic)

**Files:**
- Create: `internal/cache/cache.go`
- Test: `internal/cache/cache_test.go`

**Interfaces:**
- Consumes: nothing (pure logic package).
- Produces: `cache.State` (int enum: `StateUnknown`, `StateWarm`, `StateWarning`, `StateCritical`, `StateCold`), `cache.Result{State cache.State; RemainingSeconds int; ElapsedSeconds int}`, and `cache.Evaluate(lastWrite time.Time, ttl time.Duration, now time.Time) cache.Result`. Consumed by `internal/render` (Task 7) and `main.go` (Task 8).

- [ ] **Step 1: Write the failing test**

Create `internal/cache/cache_test.go`:
```go
package cache

import (
	"testing"
	"time"
)

func TestEvaluate(t *testing.T) {
	base := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	ttl := 5 * time.Minute

	cases := []struct {
		name          string
		elapsed       time.Duration
		wantState     State
		wantRemaining int
		wantElapsed   int
	}{
		{"just written", 0, StateWarm, 300, 0},
		{"warm, plenty left", 2 * time.Minute, StateWarm, 180, 0},
		{"warning window", 4*time.Minute + 30*time.Second, StateWarning, 30, 0},
		{"critical window", 4*time.Minute + 50*time.Second, StateCritical, 10, 0},
		{"exactly at TTL", 5 * time.Minute, StateCold, 0, 0},
		{"cold, 20s past expiry", 5*time.Minute + 20*time.Second, StateCold, 0, 20},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			now := base.Add(c.elapsed)
			got := Evaluate(base, ttl, now)
			if got.State != c.wantState {
				t.Errorf("State = %v, want %v", got.State, c.wantState)
			}
			if got.RemainingSeconds != c.wantRemaining {
				t.Errorf("RemainingSeconds = %d, want %d", got.RemainingSeconds, c.wantRemaining)
			}
			if got.ElapsedSeconds != c.wantElapsed {
				t.Errorf("ElapsedSeconds = %d, want %d", got.ElapsedSeconds, c.wantElapsed)
			}
		})
	}
}

func TestEvaluate_ZeroLastWrite(t *testing.T) {
	got := Evaluate(time.Time{}, 5*time.Minute, time.Now())
	if got.State != StateUnknown {
		t.Errorf("State = %v, want StateUnknown for zero-value lastWrite", got.State)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cache/... -v`
Expected: FAIL — package/symbols undefined.

- [ ] **Step 3: Write the implementation**

Create `internal/cache/cache.go`:
```go
// Package cache computes prompt-cache TTL state from the timestamp of the
// last cache write, with no I/O — callers supply all times explicitly.
package cache

import "time"

type State int

const (
	StateUnknown State = iota
	StateWarm
	StateWarning
	StateCritical
	StateCold
)

const (
	warningThreshold  = 60 * time.Second
	criticalThreshold = 15 * time.Second
)

type Result struct {
	State            State
	RemainingSeconds int // valid when State is Warm, Warning, or Critical
	ElapsedSeconds   int // valid when State is Cold; seconds past expiry
}

// Evaluate reports the cache TTL state at time now, given the timestamp of
// the last cache write (lastWrite) and the TTL that write used.
// A zero-value lastWrite (no write observed yet) returns StateUnknown.
func Evaluate(lastWrite time.Time, ttl time.Duration, now time.Time) Result {
	if lastWrite.IsZero() {
		return Result{State: StateUnknown}
	}

	remaining := ttl - now.Sub(lastWrite)
	if remaining <= 0 {
		return Result{State: StateCold, ElapsedSeconds: int((-remaining).Seconds())}
	}

	switch {
	case remaining <= criticalThreshold:
		return Result{State: StateCritical, RemainingSeconds: int(remaining.Seconds())}
	case remaining <= warningThreshold:
		return Result{State: StateWarning, RemainingSeconds: int(remaining.Seconds())}
	default:
		return Result{State: StateWarm, RemainingSeconds: int(remaining.Seconds())}
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cache/... -v`
Expected: PASS (all cases).

- [ ] **Step 5: Commit**

```bash
git add internal/cache
git commit -m "feat: add cache TTL state evaluation"
```

---

### Task 3: `internal/contextstate` (compact-due logic)

**Files:**
- Create: `internal/contextstate/contextstate.go`
- Test: `internal/contextstate/contextstate_test.go`

**Interfaces:**
- Consumes: nothing (pure logic package).
- Produces: `contextstate.State` (int enum: `StateUnknownCtx`, `StateOK`, `StateCompactDue`), `contextstate.Evaluate(usedPct *float64, thresholdPct float64) contextstate.State`. Consumed by `internal/render` (Task 7) and `main.go` (Task 8).

- [ ] **Step 1: Write the failing test**

Create `internal/contextstate/contextstate_test.go`:
```go
package contextstate

import "testing"

func f(v float64) *float64 { return &v }

func TestEvaluate(t *testing.T) {
	cases := []struct {
		name      string
		usedPct   *float64
		threshold float64
		want      State
	}{
		{"nil usedPct is unknown", nil, 80, StateUnknownCtx},
		{"well under threshold", f(8.5), 80, StateOK},
		{"just under threshold", f(79.9), 80, StateOK},
		{"exactly at threshold", f(80), 80, StateCompactDue},
		{"over threshold", f(92), 80, StateCompactDue},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Evaluate(c.usedPct, c.threshold)
			if got != c.want {
				t.Errorf("Evaluate(%v, %v) = %v, want %v", c.usedPct, c.threshold, got, c.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/contextstate/... -v`
Expected: FAIL — package/symbols undefined.

- [ ] **Step 3: Write the implementation**

Create `internal/contextstate/contextstate.go`:
```go
// Package contextstate decides whether context-window usage has crossed the
// user's configured compact/clear threshold. usedPct comes from Claude
// Code's own context_window.used_percentage, which is already normalized
// 0-100 regardless of the model's actual window size, so no separate
// window-size scaling is needed here.
package contextstate

type State int

const (
	StateUnknownCtx State = iota
	StateOK
	StateCompactDue
)

// Evaluate returns StateCompactDue once usedPct reaches thresholdPct.
// A nil usedPct (context_window not yet populated) returns StateUnknownCtx.
func Evaluate(usedPct *float64, thresholdPct float64) State {
	if usedPct == nil {
		return StateUnknownCtx
	}
	if *usedPct >= thresholdPct {
		return StateCompactDue
	}
	return StateOK
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/contextstate/... -v`
Expected: PASS (all cases).

- [ ] **Step 5: Commit**

```bash
git add internal/contextstate
git commit -m "feat: add context compact-threshold evaluation"
```

---

### Task 4: `internal/config` (optional user config file)

**Files:**
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `config.Config{CompactThresholdPct float64; CacheTTLOverride string; Color bool}`, `config.Default() config.Config`, `config.Load(path string) (config.Config, error)`, `config.Config.TTLOverrideDuration() (time.Duration, bool)`. Consumed by `main.go` (Task 8).

- [ ] **Step 1: Write the failing test**

Create `internal/config/config_test.go`:
```go
package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoad_MissingFileReturnsDefaults(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("Load returned error for missing file: %v", err)
	}
	want := Default()
	if got != want {
		t.Errorf("Load(missing) = %+v, want defaults %+v", got, want)
	}
}

func TestLoad_PartialOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "statusline-config.json")
	if err := os.WriteFile(path, []byte(`{"compact_threshold_pct": 70}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if got.CompactThresholdPct != 70 {
		t.Errorf("CompactThresholdPct = %v, want 70", got.CompactThresholdPct)
	}
	if got.Color != Default().Color {
		t.Errorf("Color = %v, want default %v (unspecified field should keep default)", got.Color, Default().Color)
	}
}

func TestLoad_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "statusline-config.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected an error for invalid JSON config, got nil")
	}
}

func TestTTLOverrideDuration(t *testing.T) {
	c := Config{CacheTTLOverride: "1h"}
	got, ok := c.TTLOverrideDuration()
	if !ok || got != time.Hour {
		t.Errorf("TTLOverrideDuration() = %v, %v; want 1h, true", got, ok)
	}

	c = Config{CacheTTLOverride: ""}
	if _, ok := c.TTLOverrideDuration(); ok {
		t.Error("TTLOverrideDuration() ok = true for empty override, want false")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/... -v`
Expected: FAIL — package/symbols undefined.

- [ ] **Step 3: Write the implementation**

Create `internal/config/config.go`:
```go
// Package config loads the optional user-editable statusline settings file.
// A missing file is not an error: Default() values apply.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type Config struct {
	CompactThresholdPct float64 `json:"compact_threshold_pct"`
	CacheTTLOverride    string  `json:"cache_ttl_override"`
	Color               bool    `json:"color"`
}

// Default returns the built-in settings used when no config file exists or
// a field is left unspecified.
func Default() Config {
	return Config{
		CompactThresholdPct: 80,
		CacheTTLOverride:    "",
		Color:               true,
	}
}

// Load reads the config file at path, merging any present fields onto the
// defaults. A missing file returns Default() with a nil error.
func Load(path string) (Config, error) {
	cfg := Default()

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("config: read %s: %w", path, err)
	}

	// Decode into a partial struct so an absent field doesn't overwrite a
	// default with a JSON zero value (e.g. an unset "color" would otherwise
	// clobber Default().Color=true with false).
	var partial struct {
		CompactThresholdPct *float64 `json:"compact_threshold_pct"`
		CacheTTLOverride    *string  `json:"cache_ttl_override"`
		Color               *bool    `json:"color"`
	}
	if err := json.Unmarshal(data, &partial); err != nil {
		return Config{}, fmt.Errorf("config: parse %s: %w", path, err)
	}

	if partial.CompactThresholdPct != nil {
		cfg.CompactThresholdPct = *partial.CompactThresholdPct
	}
	if partial.CacheTTLOverride != nil {
		cfg.CacheTTLOverride = *partial.CacheTTLOverride
	}
	if partial.Color != nil {
		cfg.Color = *partial.Color
	}
	return cfg, nil
}

// TTLOverrideDuration parses CacheTTLOverride (e.g. "5m", "1h") if set.
// ok is false when no override is configured or it fails to parse.
func (c Config) TTLOverrideDuration() (time.Duration, bool) {
	if c.CacheTTLOverride == "" {
		return 0, false
	}
	d, err := time.ParseDuration(c.CacheTTLOverride)
	if err != nil {
		return 0, false
	}
	return d, true
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/... -v`
Expected: PASS (all cases).

- [ ] **Step 5: Commit**

```bash
git add internal/config
git commit -m "feat: add optional statusline-config.json loader"
```

---

### Task 5: `internal/transcript` (last assistant timestamp + detected TTL)

**Files:**
- Create: `internal/transcript/transcript.go`
- Test: `internal/transcript/transcript_test.go`
- Test fixtures: `internal/transcript/testdata/basic.jsonl`, `internal/transcript/testdata/mixed_ttl.jsonl`, `internal/transcript/testdata/no_assistant.jsonl`, `internal/transcript/testdata/trailing_garbage.jsonl`

**Interfaces:**
- Consumes: a file path (the `transcript_path` field from `internal/input.Input`).
- Produces: `transcript.Usage{Timestamp time.Time; TTL time.Duration}`, `transcript.ErrNoAssistantMessage`, `transcript.LastAssistantUsage(path string) (transcript.Usage, error)`. Consumed by `main.go` (Task 8). `TTL` is `0` when it can't be determined from the data (caller should fall back to a default or config override).

- [ ] **Step 1: Create fixture files**

Create `internal/transcript/testdata/basic.jsonl` (two lines: a user turn, then an assistant turn with a 5-minute cache write):
```
{"type":"user","timestamp":"2026-07-19T12:00:00.000Z","message":{"role":"user","content":"hi"}}
{"type":"assistant","timestamp":"2026-07-19T12:00:05.123Z","message":{"role":"assistant","usage":{"input_tokens":2,"cache_creation_input_tokens":701,"cache_read_input_tokens":90425,"output_tokens":339,"cache_creation":{"ephemeral_5m_input_tokens":701,"ephemeral_1h_input_tokens":0}}}}
```

Create `internal/transcript/testdata/mixed_ttl.jsonl` (an earlier 5m write, then the most recent write using 1h — the most recent one must win):
```
{"type":"assistant","timestamp":"2026-07-19T12:00:05.000Z","message":{"role":"assistant","usage":{"cache_creation":{"ephemeral_5m_input_tokens":500,"ephemeral_1h_input_tokens":0}}}}
{"type":"assistant","timestamp":"2026-07-19T12:05:10.000Z","message":{"role":"assistant","usage":{"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":300}}}}
```

Create `internal/transcript/testdata/no_assistant.jsonl` (only non-assistant lines — fresh session, first turn still in flight):
```
{"type":"user","timestamp":"2026-07-19T12:00:00.000Z","message":{"role":"user","content":"hi"}}
{"type":"system","timestamp":"2026-07-19T12:00:00.100Z"}
```

Create `internal/transcript/testdata/trailing_garbage.jsonl` (a valid assistant line followed by a truncated/malformed line, simulating a crash mid-write):
```
{"type":"assistant","timestamp":"2026-07-19T12:00:05.000Z","message":{"role":"assistant","usage":{"cache_creation":{"ephemeral_5m_input_tokens":701,"ephemeral_1h_input_tokens":0}}}}
{"type":"assistant","timestamp":"2026-07-19T12:01:00.000Z","message":{"role":"ass
```

- [ ] **Step 2: Write the failing test**

Create `internal/transcript/transcript_test.go`:
```go
package transcript

import (
	"errors"
	"testing"
	"time"
)

func TestLastAssistantUsage_Basic(t *testing.T) {
	got, err := LastAssistantUsage("testdata/basic.jsonl")
	if err != nil {
		t.Fatalf("LastAssistantUsage returned error: %v", err)
	}
	wantTime := time.Date(2026, 7, 19, 12, 0, 5, 123_000_000, time.UTC)
	if !got.Timestamp.Equal(wantTime) {
		t.Errorf("Timestamp = %v, want %v", got.Timestamp, wantTime)
	}
	if got.TTL != 5*time.Minute {
		t.Errorf("TTL = %v, want 5m", got.TTL)
	}
}

func TestLastAssistantUsage_MixedTTL_UsesMostRecent(t *testing.T) {
	got, err := LastAssistantUsage("testdata/mixed_ttl.jsonl")
	if err != nil {
		t.Fatalf("LastAssistantUsage returned error: %v", err)
	}
	if got.TTL != time.Hour {
		t.Errorf("TTL = %v, want 1h (the later write's TTL)", got.TTL)
	}
}

func TestLastAssistantUsage_NoAssistantMessage(t *testing.T) {
	_, err := LastAssistantUsage("testdata/no_assistant.jsonl")
	if !errors.Is(err, ErrNoAssistantMessage) {
		t.Errorf("err = %v, want ErrNoAssistantMessage", err)
	}
}

func TestLastAssistantUsage_TrailingGarbageIgnored(t *testing.T) {
	got, err := LastAssistantUsage("testdata/trailing_garbage.jsonl")
	if err != nil {
		t.Fatalf("LastAssistantUsage returned error: %v", err)
	}
	wantTime := time.Date(2026, 7, 19, 12, 0, 5, 0, time.UTC)
	if !got.Timestamp.Equal(wantTime) {
		t.Errorf("Timestamp = %v, want %v (last VALID line, garbage skipped)", got.Timestamp, wantTime)
	}
}

func TestLastAssistantUsage_MissingFile(t *testing.T) {
	if _, err := LastAssistantUsage("testdata/does-not-exist.jsonl"); err == nil {
		t.Fatal("expected an error for a missing file, got nil")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/transcript/... -v`
Expected: FAIL — package/symbols undefined.

- [ ] **Step 4: Write the implementation**

Create `internal/transcript/transcript.go`:
```go
// Package transcript reads a Claude Code session transcript (JSONL) to find
// the timestamp and detected cache TTL of the most recent assistant
// message. This is the only place that idle-time-since-last-write is
// observable, since the statusline JSON itself carries no such field.
package transcript

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

var ErrNoAssistantMessage = errors.New("transcript: no assistant message found")

type Usage struct {
	Timestamp time.Time
	// TTL is the cache TTL detected from the last assistant message's
	// cache_creation breakdown. Zero when it can't be determined (e.g. the
	// write used neither ephemeral field, or both are zero because the
	// message was a full cache read with no new write).
	TTL time.Duration
}

type transcriptLine struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Message   struct {
		Usage struct {
			CacheCreation struct {
				Ephemeral5m int64 `json:"ephemeral_5m_input_tokens"`
				Ephemeral1h int64 `json:"ephemeral_1h_input_tokens"`
			} `json:"cache_creation"`
		} `json:"usage"`
	} `json:"message"`
}

// LastAssistantUsage scans the transcript at path for the most recent line
// with "type":"assistant" and returns its timestamp and detected TTL.
// Malformed trailing lines (e.g. a transcript truncated mid-write) are
// skipped rather than treated as fatal, since only the last VALID assistant
// line matters.
func LastAssistantUsage(path string) (Usage, error) {
	f, err := os.Open(path)
	if err != nil {
		return Usage{}, fmt.Errorf("transcript: open %s: %w", path, err)
	}
	defer f.Close()

	var found bool
	var result Usage

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024) // lines can be long
	for scanner.Scan() {
		var line transcriptLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			continue // skip malformed/truncated lines
		}
		if line.Type != "assistant" {
			continue
		}
		ts, err := time.Parse(time.RFC3339Nano, line.Timestamp)
		if err != nil {
			continue
		}

		ttl := time.Duration(0)
		switch {
		case line.Message.Usage.CacheCreation.Ephemeral1h > 0:
			ttl = time.Hour
		case line.Message.Usage.CacheCreation.Ephemeral5m > 0:
			ttl = 5 * time.Minute
		}

		result = Usage{Timestamp: ts, TTL: ttl}
		found = true
	}
	if err := scanner.Err(); err != nil {
		return Usage{}, fmt.Errorf("transcript: scan %s: %w", path, err)
	}
	if !found {
		return Usage{}, ErrNoAssistantMessage
	}
	return result, nil
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/transcript/... -v`
Expected: PASS (all cases).

- [ ] **Step 6: Commit**

```bash
git add internal/transcript
git commit -m "feat: add transcript scan for last assistant timestamp and TTL"
```

---

### Task 6: `internal/gitstatus` (branch/staged/modified, session-cached)

**Files:**
- Create: `internal/gitstatus/gitstatus.go`
- Test: `internal/gitstatus/gitstatus_test.go`

**Interfaces:**
- Consumes: a directory path, a `context.Context` for timeout.
- Produces: `gitstatus.Status{IsRepo bool; Branch string; Staged int; Modified int}`, `gitstatus.Get(ctx context.Context, dir string) (gitstatus.Status, error)`, `gitstatus.CachedGet(ctx context.Context, dir, sessionID, cacheDir string, maxAge time.Duration) (gitstatus.Status, error)`. Consumed by `main.go` (Task 8).

- [ ] **Step 1: Write the failing test**

Create `internal/gitstatus/gitstatus_test.go`:
```go
package gitstatus

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func TestGet_NotARepo(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	got, err := Get(ctx, dir)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.IsRepo {
		t.Errorf("IsRepo = true, want false for a plain directory")
	}
}

func TestGet_RepoWithChanges(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "test")

	tracked := filepath.Join(dir, "tracked.txt")
	if err := os.WriteFile(tracked, []byte("v1\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	runGit(t, dir, "add", "tracked.txt")
	runGit(t, dir, "commit", "-m", "initial")

	// One staged new file, one unstaged modification to the tracked file.
	if err := os.WriteFile(tracked, []byte("v2\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	staged := filepath.Join(dir, "staged.txt")
	if err := os.WriteFile(staged, []byte("new\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	runGit(t, dir, "add", "staged.txt")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	got, err := Get(ctx, dir)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if !got.IsRepo {
		t.Fatal("IsRepo = false, want true")
	}
	if got.Branch != "main" {
		t.Errorf("Branch = %q, want %q", got.Branch, "main")
	}
	if got.Staged != 1 {
		t.Errorf("Staged = %d, want 1", got.Staged)
	}
	if got.Modified != 1 {
		t.Errorf("Modified = %d, want 1", got.Modified)
	}
}

func TestCachedGet_ReusesWithinMaxAge(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	cacheDir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	first, err := CachedGet(ctx, dir, "session-a", cacheDir, 5*time.Second)
	if err != nil {
		t.Fatalf("CachedGet (first) returned error: %v", err)
	}

	// Create a second branch on disk but don't check it out; if CachedGet
	// re-ran git it would still report "main", so this only proves the
	// cache file was written and is reused without erroring on a second call.
	second, err := CachedGet(ctx, dir, "session-a", cacheDir, 5*time.Second)
	if err != nil {
		t.Fatalf("CachedGet (second) returned error: %v", err)
	}
	if first != second {
		t.Errorf("second call = %+v, want identical to first %+v", second, first)
	}

	cacheFile := filepath.Join(cacheDir, "statusline-git-cache-session-a")
	if _, err := os.Stat(cacheFile); err != nil {
		t.Errorf("expected cache file %s to exist: %v", cacheFile, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/gitstatus/... -v`
Expected: FAIL — package/symbols undefined. (Requires `git` on PATH; if unavailable, note it and continue — CI/dev machines used for this project have git installed.)

- [ ] **Step 3: Write the implementation**

Create `internal/gitstatus/gitstatus.go`:
```go
// Package gitstatus shells out to git for branch and change-count info,
// with a session-scoped file cache so a slow repo doesn't stall every
// statusline refresh. Caching is keyed by session_id, not PID: the
// statusline binary re-execs on every refresh, so a PID-keyed cache would
// never hit.
package gitstatus

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Status struct {
	IsRepo   bool
	Branch   string
	Staged   int
	Modified int
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return out.String(), nil
}

func countLines(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	return len(strings.Split(s, "\n"))
}

// Get inspects dir directly (no caching). A non-repo directory returns
// Status{IsRepo: false} with a nil error, not an error — that's an expected
// outcome, not a failure.
func Get(ctx context.Context, dir string) (Status, error) {
	if _, err := runGit(ctx, dir, "rev-parse", "--git-dir"); err != nil {
		return Status{IsRepo: false}, nil
	}

	branch, err := runGit(ctx, dir, "branch", "--show-current")
	if err != nil {
		return Status{}, fmt.Errorf("gitstatus: branch: %w", err)
	}

	staged, err := runGit(ctx, dir, "diff", "--cached", "--numstat")
	if err != nil {
		return Status{}, fmt.Errorf("gitstatus: staged diff: %w", err)
	}

	modified, err := runGit(ctx, dir, "diff", "--numstat")
	if err != nil {
		return Status{}, fmt.Errorf("gitstatus: modified diff: %w", err)
	}

	return Status{
		IsRepo:   true,
		Branch:   strings.TrimSpace(branch),
		Staged:   countLines(staged),
		Modified: countLines(modified),
	}, nil
}

// CachedGet returns a cached Status for (dir, sessionID) if the cache file
// in cacheDir is younger than maxAge, otherwise calls Get and refreshes it.
func CachedGet(ctx context.Context, dir, sessionID, cacheDir string, maxAge time.Duration) (Status, error) {
	cacheFile := filepath.Join(cacheDir, "statusline-git-cache-"+sessionID)

	if info, err := os.Stat(cacheFile); err == nil {
		if time.Since(info.ModTime()) <= maxAge {
			if data, err := os.ReadFile(cacheFile); err == nil {
				if s, ok := decodeStatus(string(data)); ok {
					return s, nil
				}
			}
		}
	}

	status, err := Get(ctx, dir)
	if err != nil {
		return Status{}, err
	}
	_ = os.WriteFile(cacheFile, []byte(encodeStatus(status)), 0o644) // best-effort
	return status, nil
}

func encodeStatus(s Status) string {
	repo := "0"
	if s.IsRepo {
		repo = "1"
	}
	return fmt.Sprintf("%s|%s|%d|%d", repo, s.Branch, s.Staged, s.Modified)
}

func decodeStatus(data string) (Status, bool) {
	parts := strings.SplitN(strings.TrimSpace(data), "|", 4)
	if len(parts) != 4 {
		return Status{}, false
	}
	var s Status
	s.IsRepo = parts[0] == "1"
	s.Branch = parts[1]
	if _, err := fmt.Sscanf(parts[2], "%d", &s.Staged); err != nil {
		return Status{}, false
	}
	if _, err := fmt.Sscanf(parts[3], "%d", &s.Modified); err != nil {
		return Status{}, false
	}
	return s, true
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/gitstatus/... -v`
Expected: PASS (all cases).

- [ ] **Step 5: Commit**

```bash
git add internal/gitstatus
git commit -m "feat: add session-cached git branch/status lookup"
```

---

### Task 7: `internal/render` (build the 2-3 output lines)

**Files:**
- Create: `internal/render/render.go`
- Test: `internal/render/render_test.go`

**Interfaces:**
- Consumes: `cache.State`/`cache.Result` (Task 2), `contextstate.State` (Task 3), `gitstatus.Status` (Task 6).
- Produces: `render.Data` struct (fields below), `render.Lines(d render.Data) []string`. Consumed by `main.go` (Task 8).

- [ ] **Step 1: Write the failing test**

Create `internal/render/render_test.go`:
```go
package render

import (
	"strings"
	"testing"

	"statusline/internal/cache"
	"statusline/internal/contextstate"
	"statusline/internal/gitstatus"
)

func pf(v float64) *float64 { return &v }

func TestLines_NoColorBasicSession(t *testing.T) {
	d := Data{
		ModelName:       "Opus",
		Dir:             "/home/user/project",
		Git:             gitstatus.Status{IsRepo: true, Branch: "main", Staged: 2, Modified: 1},
		CostUSD:         1.23,
		ContextUsedPct:  pf(8.5),
		ContextState:    contextstate.StateOK,
		CacheResult:     cache.Result{State: cache.StateWarm, RemainingSeconds: 250},
		CacheHitRatePct: pf(92.3),
		Color:           false,
	}

	lines := Lines(d)
	if len(lines) != 2 {
		t.Fatalf("len(lines) = %d, want 2 (no alert condition active); got %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "[Opus]") || !strings.Contains(lines[0], "project") || !strings.Contains(lines[0], "main") {
		t.Errorf("line 1 = %q, missing expected content", lines[0])
	}
	if !strings.Contains(lines[1], "8%") || !strings.Contains(lines[1], "$1.23") || !strings.Contains(lines[1], "92%") {
		t.Errorf("line 2 = %q, missing expected content", lines[1])
	}
}

func TestLines_CriticalCacheTakesPriorityOverCompactDue(t *testing.T) {
	d := Data{
		ModelName:      "Opus",
		Dir:            "/home/user/project",
		ContextUsedPct: pf(85),
		ContextState:   contextstate.StateCompactDue,
		CacheResult:    cache.Result{State: cache.StateCritical, RemainingSeconds: 10},
		Color:          false,
	}

	lines := Lines(d)
	if len(lines) != 3 {
		t.Fatalf("len(lines) = %d, want 3 (one alert line); got %v", len(lines), lines)
	}
	if !strings.Contains(lines[2], "10s") {
		t.Errorf("line 3 = %q, want the critical cache warning, not the compact-due warning", lines[2])
	}
}

func TestLines_CompactDueShownWhenCacheIsFine(t *testing.T) {
	d := Data{
		ModelName:      "Opus",
		Dir:            "/home/user/project",
		ContextUsedPct: pf(85),
		ContextState:   contextstate.StateCompactDue,
		CacheResult:    cache.Result{State: cache.StateWarm, RemainingSeconds: 200},
		Color:          false,
	}

	lines := Lines(d)
	if len(lines) != 3 {
		t.Fatalf("len(lines) = %d, want 3; got %v", len(lines), lines)
	}
	if !strings.Contains(lines[2], "compact") && !strings.Contains(lines[2], "/clear") {
		t.Errorf("line 3 = %q, want a compact/clear recommendation", lines[2])
	}
}

func TestLines_ColdCacheShowsDistinctMessage(t *testing.T) {
	d := Data{
		ModelName:    "Opus",
		Dir:          "/home/user/project",
		CacheResult:  cache.Result{State: cache.StateCold, ElapsedSeconds: 30},
		ContextState: contextstate.StateOK,
		Color:        false,
	}

	lines := Lines(d)
	if len(lines) != 3 {
		t.Fatalf("len(lines) = %d, want 3; got %v", len(lines), lines)
	}
	if !strings.Contains(strings.ToLower(lines[2]), "cold") {
		t.Errorf("line 3 = %q, want it to mention the cache is cold", lines[2])
	}
}

func TestLines_NoGitOmitsGitSegment(t *testing.T) {
	d := Data{
		ModelName: "Opus",
		Dir:       "/home/user/project",
		Git:       gitstatus.Status{IsRepo: false},
		Color:     false,
	}
	lines := Lines(d)
	if strings.Contains(lines[0], "🌿") {
		t.Errorf("line 1 = %q, should not include a branch segment when not a git repo", lines[0])
	}
}

func TestLines_NilContextUsedPctOmitsContextBar(t *testing.T) {
	d := Data{
		ModelName:      "Opus",
		Dir:            "/home/user/project",
		ContextUsedPct: nil,
		CostUSD:        0.10,
		Color:          false,
	}
	lines := Lines(d)
	if strings.Contains(lines[1], "%") {
		t.Errorf("line 2 = %q, should omit the context bar when used_percentage is nil (pre-first-response)", lines[1])
	}
	if !strings.Contains(lines[1], "$0.10") {
		t.Errorf("line 2 = %q, should still show cost", lines[1])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/render/... -v`
Expected: FAIL — package/symbols undefined.

- [ ] **Step 3: Write the implementation**

Create `internal/render/render.go`:
```go
// Package render builds the statusline's output lines from already-computed
// state. It has no I/O and no knowledge of Claude Code's JSON schema —
// main.go is responsible for translating input/cache/contextstate/gitstatus
// data into a Data value.
package render

import (
	"fmt"
	"path/filepath"
	"strings"

	"statusline/internal/cache"
	"statusline/internal/contextstate"
	"statusline/internal/gitstatus"
)

type Data struct {
	ModelName       string
	Dir             string
	Git             gitstatus.Status
	CostUSD         float64
	ContextUsedPct  *float64 // nil before the first API response
	ContextState    contextstate.State
	CacheResult     cache.Result
	CacheHitRatePct *float64 // nil when current_usage is unavailable
	FiveHourPct     *float64
	SevenDayPct     *float64
	Color           bool
}

const (
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiRed    = "\033[31m"
	ansiReset  = "\033[0m"
)

func colorize(d Data, code, s string) string {
	if !d.Color {
		return s
	}
	return code + s + ansiReset
}

func contextBar(pct float64, width int) string {
	filled := int(pct) * width / 100
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

func barColor(pct float64) string {
	switch {
	case pct >= 90:
		return ansiRed
	case pct >= 70:
		return ansiYellow
	default:
		return ansiGreen
	}
}

// Lines renders the statusline as 2 fixed lines plus an optional 3rd alert
// line. Only one alert condition is shown at a time, in priority order:
// critical cache > warning cache > cold cache > compact due.
func Lines(d Data) []string {
	line1 := renderLine1(d)
	line2 := renderLine2(d)
	lines := []string{line1, line2}

	if alert := renderAlertLine(d); alert != "" {
		lines = append(lines, alert)
	}
	return lines
}

func renderLine1(d Data) string {
	dirBase := filepath.Base(d.Dir)
	s := fmt.Sprintf("[%s] 📁 %s", d.ModelName, dirBase)
	if d.Git.IsRepo && d.Git.Branch != "" {
		s += fmt.Sprintf(" | 🌿 %s +%d ~%d", d.Git.Branch, d.Git.Staged, d.Git.Modified)
	}
	return s
}

func renderLine2(d Data) string {
	var parts []string

	if d.ContextUsedPct != nil {
		pct := *d.ContextUsedPct
		bar := contextBar(pct, 10)
		bar = colorize(d, barColor(pct), bar)
		parts = append(parts, fmt.Sprintf("%s %.0f%%", bar, pct))
	}

	parts = append(parts, fmt.Sprintf("$%.2f", d.CostUSD))

	if d.CacheHitRatePct != nil {
		parts = append(parts, fmt.Sprintf("cache: %.0f%%", *d.CacheHitRatePct))
	}

	var limitParts []string
	if d.FiveHourPct != nil {
		limitParts = append(limitParts, fmt.Sprintf("5h: %.0f%%", *d.FiveHourPct))
	}
	if d.SevenDayPct != nil {
		limitParts = append(limitParts, fmt.Sprintf("7d: %.0f%%", *d.SevenDayPct))
	}
	if len(limitParts) > 0 {
		parts = append(parts, strings.Join(limitParts, " "))
	}

	return strings.Join(parts, " | ")
}

func renderAlertLine(d Data) string {
	switch d.CacheResult.State {
	case cache.StateCritical:
		msg := fmt.Sprintf("🔴 cache expires in %ds — send now or pay full rewrite", d.CacheResult.RemainingSeconds)
		return colorize(d, ansiRed, msg)
	case cache.StateWarning:
		msg := fmt.Sprintf("🟡 cache expires in %ds", d.CacheResult.RemainingSeconds)
		return colorize(d, ansiYellow, msg)
	case cache.StateCold:
		return "❄️ cache cold — next message re-reads context at full price"
	}

	if d.ContextState == contextstate.StateCompactDue {
		pct := 0.0
		if d.ContextUsedPct != nil {
			pct = *d.ContextUsedPct
		}
		msg := fmt.Sprintf("⚠️ context at %.0f%% — /compact or /clear recommended", pct)
		return colorize(d, ansiYellow, msg)
	}

	return ""
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/render/... -v`
Expected: PASS (all cases).

- [ ] **Step 5: Commit**

```bash
git add internal/render
git commit -m "feat: add statusline rendering with priority-ordered alert line"
```

---

### Task 8: `main.go` (wire everything together)

**Files:**
- Create: `main.go`
- Test: `main_test.go`

**Interfaces:**
- Consumes: every package from Tasks 1–7.
- Produces: the `statusline` executable.

- [ ] **Step 1: Write the failing integration test**

Create `main_test.go`:
```go
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildBinary compiles the statusline binary once for this test file's use.
func buildBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "statusline")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}
	return bin
}

func TestMain_MinimalInputProducesOutput(t *testing.T) {
	bin := buildBinary(t)

	input := `{"model":{"display_name":"Opus"},"workspace":{"current_dir":"/tmp"},"session_id":"test-session-abc"}`
	cmd := exec.Command(bin)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("statusline exited with error: %v\noutput: %s", err, out)
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		t.Fatal("statusline produced no output for minimal input")
	}
	if !strings.Contains(string(out), "Opus") {
		t.Errorf("output = %q, want it to contain the model name", out)
	}
}

func TestMain_MissingTranscriptDoesNotFail(t *testing.T) {
	bin := buildBinary(t)

	input := `{"model":{"display_name":"Opus"},"workspace":{"current_dir":"/tmp"},"session_id":"test-session-xyz","transcript_path":"/does/not/exist.jsonl"}`
	cmd := exec.Command(bin)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("statusline exited with error for missing transcript: %v\noutput: %s", err, out)
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		t.Fatal("statusline produced no output when transcript is missing")
	}
}

func TestMain_InvalidJSONStillExitsZero(t *testing.T) {
	bin := buildBinary(t)

	cmd := exec.Command(bin)
	cmd.Stdin = strings.NewReader("not json")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("statusline must always exit 0 even on bad input (Claude Code blanks the statusline on non-zero exit); got err=%v, output=%s", err, out)
	}
	_ = out // degraded output is acceptable; just must not fail/hang
}

func TestMain_UsesHomeConfigOverride(t *testing.T) {
	bin := buildBinary(t)

	fakeHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(fakeHome, ".claude"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	cfgPath := filepath.Join(fakeHome, ".claude", "statusline-config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"compact_threshold_pct": 10, "color": false}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	input := `{"model":{"display_name":"Opus"},"workspace":{"current_dir":"/tmp"},"session_id":"test-session-cfg","context_window":{"used_percentage":50,"context_window_size":200000,"total_input_tokens":1000,"total_output_tokens":100}}`
	cmd := exec.Command(bin)
	cmd.Stdin = strings.NewReader(input)
	cmd.Env = append(os.Environ(), "HOME="+fakeHome)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("statusline exited with error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(string(out), "/compact") {
		t.Errorf("output = %q, want compact-due warning since threshold (10) is below used_percentage (50)", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test . -v`
Expected: FAIL — `main` package doesn't exist yet / build error.

- [ ] **Step 3: Write the implementation**

Create `main.go`:
```go
// Command statusline is a Claude Code statusLine.command implementation
// focused on prompt-cache cost visibility: it warns when the cache is about
// to expire, has already gone cold, or context is past the compact
// threshold. See docs/superpowers/specs/2026-07-19-statusline-design.md.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"statusline/internal/cache"
	"statusline/internal/config"
	"statusline/internal/contextstate"
	"statusline/internal/gitstatus"
	"statusline/internal/input"
	"statusline/internal/render"
	"statusline/internal/transcript"
)

const gitTimeout = 200 * time.Millisecond

func main() {
	// Per the design doc's error-handling rule: always exit 0 and always
	// print something, even in a fully degraded state, since a non-zero
	// exit or empty stdout blanks the entire Claude Code statusline.
	lines := run(os.Stdin)
	for _, l := range lines {
		fmt.Println(l)
	}
}

func run(stdin *os.File) []string {
	in, err := input.Parse(stdin)
	if err != nil {
		return []string{"[statusline: invalid input]"}
	}

	homeDir, _ := os.UserHomeDir()
	cfg, _ := config.Load(filepath.Join(homeDir, ".claude", "statusline-config.json"))

	data := render.Data{
		ModelName: in.Model.DisplayName,
		Dir:       in.Workspace.CurrentDir,
		Color:     cfg.Color,
		CostUSD:   in.Cost.TotalCostUSD,
	}
	if data.ModelName == "" {
		data.ModelName = "Claude"
	}

	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	if gs, err := gitstatus.CachedGet(ctx, in.Workspace.CurrentDir, in.SessionID, os.TempDir(), 5*time.Second); err == nil {
		data.Git = gs
	}

	if in.ContextWindow != nil {
		data.ContextUsedPct = in.ContextWindow.UsedPercentage
		if in.ContextWindow.CurrentUsage != nil {
			data.CacheHitRatePct = cacheHitRate(in.ContextWindow.CurrentUsage)
		}
	}
	data.ContextState = contextstate.Evaluate(data.ContextUsedPct, cfg.CompactThresholdPct)

	if in.RateLimits != nil {
		if in.RateLimits.FiveHour != nil {
			v := in.RateLimits.FiveHour.UsedPercentage
			data.FiveHourPct = &v
		}
		if in.RateLimits.SevenDay != nil {
			v := in.RateLimits.SevenDay.UsedPercentage
			data.SevenDayPct = &v
		}
	}

	data.CacheResult = evaluateCache(in, cfg)

	return render.Lines(data)
}

func cacheHitRate(u *input.CurrentUsage) *float64 {
	total := u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
	if total == 0 {
		return nil
	}
	rate := float64(u.CacheReadInputTokens) / float64(total) * 100
	return &rate
}

func evaluateCache(in *input.Input, cfg config.Config) cache.Result {
	if in.TranscriptPath == "" {
		return cache.Result{State: cache.StateUnknown}
	}

	usage, err := transcript.LastAssistantUsage(in.TranscriptPath)
	if err != nil {
		return cache.Result{State: cache.StateUnknown}
	}

	ttl := usage.TTL
	if ttl == 0 {
		if override, ok := cfg.TTLOverrideDuration(); ok {
			ttl = override
		} else {
			ttl = 5 * time.Minute
		}
	}

	return cache.Evaluate(usage.Timestamp, ttl, time.Now())
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test . -v`
Expected: PASS (all four integration tests).

- [ ] **Step 5: Run the full test suite**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: build succeeds, vet is clean, all package tests PASS.

- [ ] **Step 6: Manual smoke test with mock input**

Run:
```bash
go build -o statusline .
echo '{"model":{"display_name":"Opus"},"workspace":{"current_dir":"/home/user/project"},"context_window":{"used_percentage":25,"context_window_size":200000},"session_id":"test-session-abc","cost":{"total_cost_usd":0.42}}' | ./statusline
```
Expected: two lines printed, e.g.:
```
[Opus] 📁 project
██░░░░░░░░ 25% | $0.42
```
(No 🌿 branch segment since `/home/user/project` isn't a real repo on the test machine; no cache line since no `transcript_path` was given.)

- [ ] **Step 7: Commit**

```bash
git add main.go main_test.go
git commit -m "feat: wire input, cache, context, git, and config into the statusline binary"
```

---

### Task 9: Install docs + example Claude Code settings snippet

**Files:**
- Create: `README.md`
- Create: `examples/statusline-config.json`

**Interfaces:**
- Consumes: nothing (documentation only).
- Produces: nothing consumed by other tasks — this is the last task.

- [ ] **Step 1: Write the example user config file**

Create `examples/statusline-config.json`:
```json
{
  "compact_threshold_pct": 80,
  "cache_ttl_override": "",
  "color": true
}
```

- [ ] **Step 2: Write the README**

Create `README.md`:
```markdown
# statusline

A Claude Code statusline focused on prompt-cache cost visibility: it warns
when the cache is about to expire, has already gone cold, or context is past
the compact/clear threshold, instead of just showing raw cost and context
numbers. Background and rationale: `docs/superpowers/specs/2026-07-19-statusline-design.md`.

## Build

```bash
go build -o statusline .
```

## Install

1. Copy (or symlink) the built binary somewhere stable, e.g. `~/.claude/statusline`.
2. Add it to your Claude Code settings (`~/.claude/settings.json`):

```json
{
  "statusLine": {
    "type": "command",
    "command": "~/.claude/statusline",
    "refreshInterval": 15
  }
}
```

`refreshInterval` is required for the cache-expiry countdown to advance while
you're idle — without it, the statusline only updates after you send a
message, which defeats the early warning.

## Configuration (optional)

Create `~/.claude/statusline-config.json` (see `examples/statusline-config.json`)
to override defaults:

| Field | Default | Meaning |
| --- | --- | --- |
| `compact_threshold_pct` | `80` | Context `used_percentage` at which the third line recommends `/compact` or `/clear`. |
| `cache_ttl_override` | `""` (unset) | Fallback TTL (e.g. `"5m"`, `"1h"`) used only when the TTL can't be auto-detected from the transcript (e.g. no assistant message yet this session). Auto-detection from the transcript's `cache_creation` breakdown is authoritative when available. |
| `color` | `true` | Set `false` to disable ANSI color codes. |

Missing file or missing fields fall back to defaults — no config file is required.

## Output

Two lines always; a third appears only when action is worth taking:

```
[Opus] 📁 my-project | 🌿 main +2 ~1
██████████ 82% | $3.41 | cache: 91% | 5h: 23% 7d: 41%
⚠️ context at 82% — /compact or /clear recommended
```

or, when the cache is about to go cold from idle:

```
[Opus] 📁 my-project | 🌿 main
███░░░░░░░ 30% | $1.10
🟡 cache expires in 47s
```
```

- [ ] **Step 3: Verify the README's build/install commands actually work**

Run:
```bash
go build -o statusline .
echo '{"model":{"display_name":"Opus"},"workspace":{"current_dir":"'"$PWD"'"},"session_id":"readme-check"}' | ./statusline
rm ./statusline
```
Expected: prints a two-line statusline with no errors; confirms the README's documented commands are accurate as written.

- [ ] **Step 4: Commit**

```bash
git add README.md examples/statusline-config.json
git commit -m "docs: add README with install and configuration instructions"
```
