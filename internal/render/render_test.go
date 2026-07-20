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

func TestLines_ColdCacheColorsRedWhenColorEnabled(t *testing.T) {
	d := Data{
		ModelName:    "Opus",
		Dir:          "/home/user/project",
		CacheResult:  cache.Result{State: cache.StateCold, ElapsedSeconds: 30},
		ContextState: contextstate.StateOK,
		Color:        true,
	}
	lines := Lines(d)
	if !strings.Contains(lines[2], ansiRed) {
		t.Errorf("line 3 = %q, want cold alert wrapped in ansiRed when Color is true", lines[2])
	}
}

func TestLines_DirtyGitBranchColorsRedWhenColorEnabled(t *testing.T) {
	d := Data{
		ModelName: "Opus",
		Dir:       "/home/user/project",
		Git:       gitstatus.Status{IsRepo: true, Branch: "main", Staged: 1, Modified: 0},
		Color:     true,
	}
	lines := Lines(d)
	if !strings.Contains(lines[0], ansiRed+"main"+ansiReset) {
		t.Errorf("line 1 = %q, want branch name wrapped in ansiRed when dirty", lines[0])
	}
}

func TestLines_CleanGitBranchNotColored(t *testing.T) {
	d := Data{
		ModelName: "Opus",
		Dir:       "/home/user/project",
		Git:       gitstatus.Status{IsRepo: true, Branch: "main", Staged: 0, Modified: 0},
		Color:     true,
	}
	lines := Lines(d)
	if strings.Contains(lines[0], ansiRed) {
		t.Errorf("line 1 = %q, want no color on a clean branch", lines[0])
	}
}

func TestLines_RateLimitPctColorRamped(t *testing.T) {
	d := Data{
		ModelName:   "Opus",
		Dir:         "/home/user/project",
		FiveHourPct: pf(95),
		SevenDayPct: pf(50),
		Color:       true,
	}
	lines := Lines(d)
	if !strings.Contains(lines[1], ansiRed+"5h: 95%"+ansiReset) {
		t.Errorf("line 2 = %q, want 5h pct wrapped in ansiRed at 95%%", lines[1])
	}
	if !strings.Contains(lines[1], ansiGreen+"7d: 50%"+ansiReset) {
		t.Errorf("line 2 = %q, want 7d pct wrapped in ansiGreen at 50%%", lines[1])
	}
}

func TestLines_CacheHitRateInvertedColorRamp(t *testing.T) {
	d := Data{
		ModelName:       "Opus",
		Dir:             "/home/user/project",
		CacheHitRatePct: pf(20),
		Color:           true,
	}
	lines := Lines(d)
	if !strings.Contains(lines[1], ansiRed+"cache: 20%"+ansiReset) {
		t.Errorf("line 2 = %q, want low cache hit-rate (20%%) wrapped in ansiRed", lines[1])
	}
}

func TestLines_CacheHitRateHighIsGreen(t *testing.T) {
	d := Data{
		ModelName:       "Opus",
		Dir:             "/home/user/project",
		CacheHitRatePct: pf(85),
		Color:           true,
	}
	lines := Lines(d)
	if !strings.Contains(lines[1], ansiGreen+"cache: 85%"+ansiReset) {
		t.Errorf("line 2 = %q, want high cache hit-rate (85%%) wrapped in ansiGreen", lines[1])
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
