package db

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestLockoutRemaining(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) *time.Time { t := now.Add(-d); return &t }
	for _, tc := range []struct {
		name     string
		attempts int
		last     *time.Time
		want     time.Duration
	}{
		{name: "no failures", attempts: 0, want: 0},
		{name: "below threshold", attempts: 4, last: ago(0), want: 0},
		{name: "fifth failure locks one minute", attempts: 5, last: ago(0), want: time.Minute},
		{name: "sixth failure doubles", attempts: 6, last: ago(0), want: 2 * time.Minute},
		{name: "partially elapsed", attempts: 6, last: ago(30 * time.Second), want: 90 * time.Second},
		{name: "expired", attempts: 5, last: ago(time.Minute), want: 0},
		{name: "capped at one hour", attempts: 11, last: ago(0), want: time.Hour},
		{name: "huge count capped", attempts: 1000, last: ago(0), want: time.Hour},
		{name: "legacy permanent lock released", attempts: 10, last: ago(2 * time.Hour), want: 0},
		{name: "missing timestamp", attempts: 10, want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if diff := cmp.Diff(tc.want, LockoutRemaining(tc.attempts, tc.last, now)); diff != "" {
				t.Errorf("LockoutRemaining (-want +got):\n%s", diff)
			}
		})
	}
}
