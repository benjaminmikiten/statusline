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

const ioTimeout = 200 * time.Millisecond

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
	cfg, err := config.Load(filepath.Join(homeDir, ".claude", "statusline-config.json"))
	if err != nil {
		cfg = config.Default()
	}

	data := render.Data{
		ModelName: in.Model.DisplayName,
		Dir:       in.Workspace.CurrentDir,
		Color:     cfg.Color,
		CostUSD:   in.Cost.TotalCostUSD,
	}
	if data.ModelName == "" {
		data.ModelName = "Claude"
	}

	ctx, cancel := context.WithTimeout(context.Background(), ioTimeout)
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

	ctx, cancel := context.WithTimeout(context.Background(), ioTimeout)
	defer cancel()
	usage, err := transcript.LastAssistantUsage(ctx, in.TranscriptPath)
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
