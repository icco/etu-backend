package db

import "time"

const (
	lockoutThreshold = 5
	lockoutBase      = time.Minute
	lockoutMax       = time.Hour
)

// LockoutRemaining reports how long an account stays locked after the given
// number of consecutive failed logins, the last of which happened at last.
func LockoutRemaining(attempts int, last *time.Time, now time.Time) time.Duration {
	if attempts < lockoutThreshold || last == nil {
		return 0
	}
	window := lockoutBase
	for i := lockoutThreshold; i < attempts && window < lockoutMax; i++ {
		window *= 2
	}
	window = min(window, lockoutMax)
	return max(last.Add(window).Sub(now), 0)
}
