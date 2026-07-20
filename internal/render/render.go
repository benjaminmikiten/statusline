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

// hitRateColor ramps in the opposite direction of barColor: a high cache
// hit-rate is good (green), a low one is bad (red).
func hitRateColor(pct float64) string {
	switch {
	case pct >= 70:
		return ansiGreen
	case pct >= 30:
		return ansiYellow
	default:
		return ansiRed
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
		branch := d.Git.Branch
		if d.Git.Staged > 0 || d.Git.Modified > 0 {
			branch = colorize(d, ansiRed, branch)
		}
		s += fmt.Sprintf(" | 🌿 %s +%d ~%d", branch, d.Git.Staged, d.Git.Modified)
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
		pct := *d.CacheHitRatePct
		s := colorize(d, hitRateColor(pct), fmt.Sprintf("cache: %.0f%%", pct))
		parts = append(parts, s)
	}

	var limitParts []string
	if d.FiveHourPct != nil {
		pct := *d.FiveHourPct
		limitParts = append(limitParts, colorize(d, barColor(pct), fmt.Sprintf("5h: %.0f%%", pct)))
	}
	if d.SevenDayPct != nil {
		pct := *d.SevenDayPct
		limitParts = append(limitParts, colorize(d, barColor(pct), fmt.Sprintf("7d: %.0f%%", pct)))
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
		return colorize(d, ansiRed, "❄️ cache cold — next message re-reads context at full price")
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
