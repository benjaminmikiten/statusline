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
