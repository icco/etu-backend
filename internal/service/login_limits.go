package service

import (
	"crypto/sha256"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Fixed hash buckets bound memory even when callers submit unlimited account
// names. Collisions share a quota rather than bypassing the limit. These limits
// are per server instance; the database session cap is shared across instances.
type loginLimits struct {
	mu       sync.Mutex
	accounts [4096]*rate.Limiter
	global   *rate.Limiter
	inFlight chan struct{}
}

func newLoginLimits() *loginLimits {
	return &loginLimits{global: rate.NewLimiter(rate.Every(time.Second), 10), inFlight: make(chan struct{}, 4)}
}

func (l *loginLimits) acquire(email string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.global.AllowN(now, 1) {
		return false
	}
	hash := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	index := (int(hash[0])<<8 | int(hash[1])) % len(l.accounts)
	if l.accounts[index] == nil {
		l.accounts[index] = rate.NewLimiter(rate.Every(12*time.Second), 5)
	}
	if !l.accounts[index].AllowN(now, 1) {
		return false
	}
	select {
	case l.inFlight <- struct{}{}:
		return true
	default:
		return false
	}
}

func (l *loginLimits) release() { <-l.inFlight }
