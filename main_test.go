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

func TestMain_MalformedConfigFallsBackToDefaults(t *testing.T) {
	bin := buildBinary(t)

	fakeHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(fakeHome, ".claude"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	cfgPath := filepath.Join(fakeHome, ".claude", "statusline-config.json")
	if err := os.WriteFile(cfgPath, []byte(`not json`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// used_percentage (50) is below the real default threshold (80), but
	// above the zero-value threshold that a discarded config.Load error
	// would silently fall back to. If main.go used cfg.CompactThresholdPct
	// == 0 from a zero-value Config, this would spuriously fire a
	// compact-due warning.
	input := `{"model":{"display_name":"Opus"},"workspace":{"current_dir":"/tmp"},"session_id":"test-session-malformed-cfg","context_window":{"used_percentage":50,"context_window_size":200000,"total_input_tokens":1000,"total_output_tokens":100}}`
	cmd := exec.Command(bin)
	cmd.Stdin = strings.NewReader(input)
	cmd.Env = append(os.Environ(), "HOME="+fakeHome)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("statusline exited with error: %v\noutput: %s", err, out)
	}
	if strings.Contains(string(out), "/compact") {
		t.Errorf("output = %q, want no compact-due warning: malformed config should fall back to config.Default() (threshold 80), not zero-value threshold 0", out)
	}
}
