package attemptlimit

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var now = time.Date(2026, time.November, 1, 12, 0, 0, 0, time.UTC)

// ago returns the times d before now.
func ago(d ...time.Duration) []time.Time {
	times := make([]time.Time, len(d))
	for i, age := range d {
		times[i] = now.Add(-age)
	}
	return times
}

func TestWait(t *testing.T) {
	short := Limit{Window: 15 * time.Minute, Max: 3}
	long := Limit{Window: 24 * time.Hour, Max: 5}
	for _, c := range []struct {
		name  string
		times []time.Time
		want  time.Duration
	}{
		{"none", nil, 0},
		{"under both", ago(time.Minute, 2*time.Minute), 0},
		{"short full", ago(time.Minute, 2*time.Minute, 3*time.Minute), 12 * time.Minute},
		{"order does not matter", ago(2*time.Minute, 3*time.Minute, time.Minute), 12 * time.Minute},
		{"over short", ago(time.Minute, 2*time.Minute, 3*time.Minute, 4*time.Minute), 12 * time.Minute},
		{"exactly a window old", ago(time.Minute, 2*time.Minute, 15*time.Minute), 0},
		{"just inside a window", ago(time.Minute, 2*time.Minute, 15*time.Minute-time.Nanosecond), time.Nanosecond},
		{"long full", ago(time.Hour, 2*time.Hour, 3*time.Hour, 4*time.Hour, 5*time.Hour), 19 * time.Hour},
		{"short wait wins", ago(time.Minute, 2*time.Minute, 3*time.Minute, 23*time.Hour+50*time.Minute, 23*time.Hour+55*time.Minute), 12 * time.Minute},
		{"long wait wins", ago(time.Minute, 2*time.Minute, 3*time.Minute, 4*time.Hour, 5*time.Hour), 19 * time.Hour},
		{"future time counts", []time.Time{now.Add(time.Second), now, now}, 15 * time.Minute},
	} {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, Wait(c.times, now, short, long))
		})
	}
}

// After waiting, one more attempt fits.
func TestWaitFits(t *testing.T) {
	limit := Limit{Window: 15 * time.Minute, Max: 2}
	times := ago(time.Minute, 5*time.Minute, 9*time.Minute)

	wait := Wait(times, now, limit)

	assert.Equal(t, 10*time.Minute, wait)
	assert.Zero(t, Wait(times, now.Add(wait), limit))
	assert.Positive(t, Wait(times, now.Add(wait-time.Nanosecond), limit))
}

func TestWaitNoLimits(t *testing.T) {
	assert.Zero(t, Wait(ago(time.Second), now))
}
