// Package contextstate decides whether context-window usage has crossed the
// user's configured compact/clear threshold. usedPct comes from Claude
// Code's own context_window.used_percentage, which is already normalized
// 0-100 regardless of the model's actual window size, so no separate
// window-size scaling is needed here.
package contextstate

type State int

const (
	StateUnknownCtx State = iota
	StateOK
	StateCompactDue
)

// Evaluate returns StateCompactDue once usedPct reaches thresholdPct.
// A nil usedPct (context_window not yet populated) returns StateUnknownCtx.
func Evaluate(usedPct *float64, thresholdPct float64) State {
	if usedPct == nil {
		return StateUnknownCtx
	}
	if *usedPct >= thresholdPct {
		return StateCompactDue
	}
	return StateOK
}
