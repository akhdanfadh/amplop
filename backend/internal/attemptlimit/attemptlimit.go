// Package attemptlimit decides when one more attempt fits sliding-window
// limits, such as 5 per 15 minutes and 20 per 24 hours.
//
// NOTE: each throttle keeps its own key, storage, and lock in its feature
// and passes the times it counted here, so every throttle counts a window's
// edge the same way and can say how long to wait. ADR024 records the choice.
package attemptlimit

import (
	"slices"
	"time"
)

// Error is a throttle's refusal: Err is the feature's error, such as
// session.ErrThrottled, and Wait is how long until one more attempt fits.
type Error struct {
	Err  error
	Wait time.Duration
}

// Error returns Err's text.
func (e *Error) Error() string { return e.Err.Error() }

// Unwrap returns Err, so errors.Is matches the feature's error.
func (e *Error) Unwrap() error { return e.Err }

// Limit allows at most Max attempts in any period of length Window. Max must
// be at least 1.
type Limit struct {
	Window time.Duration
	Max    int
}

// Wait returns how long after now one more attempt fits every limit, given
// the times of the attempts already counted, in any order. It returns zero
// when the attempt fits now. An attempt counts toward a limit until it is
// Window old; at exactly Window old it no longer counts.
func Wait(times []time.Time, now time.Time, limits ...Limit) time.Duration {
	var wait time.Duration
	for _, limit := range limits {
		cutoff := now.Add(-limit.Window)
		var counted []time.Time
		for _, at := range times {
			if at.After(cutoff) {
				counted = append(counted, at)
			}
		}
		if len(counted) < limit.Max {
			continue
		}
		slices.SortFunc(counted, time.Time.Compare)
		// One more fits once at most Max-1 remain, so the oldest
		// len-Max+1 must leave; this is the last of them to go.
		leaves := counted[len(counted)-limit.Max].Add(limit.Window)
		wait = max(wait, leaves.Sub(now))
	}
	return wait
}
