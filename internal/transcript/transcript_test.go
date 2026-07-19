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
