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
