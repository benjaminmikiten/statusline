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
