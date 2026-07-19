package transcript

import (
	"bufio"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLastAssistantUsage_Basic(t *testing.T) {
	got, err := LastAssistantUsage(context.Background(), "testdata/basic.jsonl")
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
	got, err := LastAssistantUsage(context.Background(), "testdata/mixed_ttl.jsonl")
	if err != nil {
		t.Fatalf("LastAssistantUsage returned error: %v", err)
	}
	if got.TTL != time.Hour {
		t.Errorf("TTL = %v, want 1h (the later write's TTL)", got.TTL)
	}
}

func TestLastAssistantUsage_NoAssistantMessage(t *testing.T) {
	_, err := LastAssistantUsage(context.Background(), "testdata/no_assistant.jsonl")
	if !errors.Is(err, ErrNoAssistantMessage) {
		t.Errorf("err = %v, want ErrNoAssistantMessage", err)
	}
}

func TestLastAssistantUsage_TrailingGarbageIgnored(t *testing.T) {
	got, err := LastAssistantUsage(context.Background(), "testdata/trailing_garbage.jsonl")
	if err != nil {
		t.Fatalf("LastAssistantUsage returned error: %v", err)
	}
	wantTime := time.Date(2026, 7, 19, 12, 0, 5, 0, time.UTC)
	if !got.Timestamp.Equal(wantTime) {
		t.Errorf("Timestamp = %v, want %v (last VALID line, garbage skipped)", got.Timestamp, wantTime)
	}
}

func TestLastAssistantUsage_MissingFile(t *testing.T) {
	if _, err := LastAssistantUsage(context.Background(), "testdata/does-not-exist.jsonl"); err == nil {
		t.Fatal("expected an error for a missing file, got nil")
	}
}

func TestLastAssistantUsage_TimeoutAborted(t *testing.T) {
	// A context that's already expired at call time hits the fast-path
	// guard at the top of LastAssistantUsage, before any file I/O or
	// scanning happens. This proves the guard clause works, but NOT that
	// the mid-scan abort (the goroutine + select + periodic ctx check that
	// actually protects against a huge/slow file, per Finding 1) does —
	// see TestLastAssistantUsage_TimeoutAbortsMidScan for that.
	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()
	// Ensure the deadline has definitely passed before we call in.
	<-ctx.Done()

	_, err := LastAssistantUsage(ctx, "testdata/basic.jsonl")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded", err)
	}
}

func TestLastAssistantUsage_TimeoutAbortsMidScan(t *testing.T) {
	// Exercises the actual protection Finding 1 asked for: a large file
	// whose scan takes measurably longer than the context's deadline must
	// be aborted partway through, not run to completion. If the goroutine,
	// the ctx.Done() select case, or the every-N-lines ctx.Err() check were
	// missing or broken, this test would hang or return a normal result
	// instead of a deadline error.
	dir := t.TempDir()
	path := filepath.Join(dir, "huge.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	w := bufio.NewWriter(f)
	// None of these lines are type "assistant", so the scan must walk the
	// entire file before it could return a normal (non-error) result —
	// giving the mid-scan ctx check every chance to fire first.
	const line = `{"type":"user","timestamp":"2026-07-19T12:00:00.000Z","message":{"role":"user","content":"hi"}}` + "\n"
	for i := 0; i < 300_000; i++ {
		if _, err := w.WriteString(line); err != nil {
			t.Fatalf("WriteString: %v", err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	f.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err = LastAssistantUsage(ctx, path)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded (scan of a 300k-line file should not complete within a 5ms budget)", err)
	}
	// Sanity check that we actually caught it mid-scan rather than, say,
	// the fast-path guard racing after some unrelated delay: the call
	// should return close to the deadline, not after however long a full
	// scan takes.
	if elapsed > 500*time.Millisecond {
		t.Errorf("LastAssistantUsage took %v to return after a 5ms deadline; want it to abort promptly", elapsed)
	}
}
