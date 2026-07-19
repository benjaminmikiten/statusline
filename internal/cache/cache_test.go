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
