// Package cache computes prompt-cache TTL state from the timestamp of the
// last cache write, with no I/O — callers supply all times explicitly.
package cache

import "time"

type State int

const (
	StateUnknown State = iota
	StateWarm
	StateWarning
	StateCritical
	StateCold
)

const (
	warningThreshold  = 60 * time.Second
	criticalThreshold = 15 * time.Second
)

type Result struct {
	State            State
	RemainingSeconds int // valid when State is Warm, Warning, or Critical
	ElapsedSeconds   int // valid when State is Cold; seconds past expiry
}

// Evaluate reports the cache TTL state at time now, given the timestamp of
// the last cache write (lastWrite) and the TTL that write used.
// A zero-value lastWrite (no write observed yet) returns StateUnknown.
func Evaluate(lastWrite time.Time, ttl time.Duration, now time.Time) Result {
	if lastWrite.IsZero() {
		return Result{State: StateUnknown}
	}

	remaining := ttl - now.Sub(lastWrite)
	if remaining <= 0 {
		return Result{State: StateCold, ElapsedSeconds: int((-remaining).Seconds())}
	}

	switch {
	case remaining <= criticalThreshold:
		return Result{State: StateCritical, RemainingSeconds: int(remaining.Seconds())}
	case remaining <= warningThreshold:
		return Result{State: StateWarning, RemainingSeconds: int(remaining.Seconds())}
	default:
		return Result{State: StateWarm, RemainingSeconds: int(remaining.Seconds())}
	}
}
