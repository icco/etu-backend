package service

import (
	"context"
	"crypto/sha256"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"go.icco.me/etu-backend/internal/auth"
	"go.icco.me/etu-backend/internal/db"
	"golang.org/x/time/rate"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

const (
	attemptsPerEmailBurst = 5
	attemptsPerIPBurst    = 20
	clientIPMetadataKey   = "x-etu-client-ip"
)

// attemptLimits throttles Register and Authenticate per client IP and per
// email using fixed hash buckets so memory stays bounded. Limits are per
// server instance.
type attemptLimits struct {
	mu     sync.Mutex
	ips    [4096]*rate.Limiter
	emails [4096]*rate.Limiter
}

func newAttemptLimits() *attemptLimits { return &attemptLimits{} }

func bucket(key string) int {
	hash := sha256.Sum256([]byte(key))
	return (int(hash[0])<<8 | int(hash[1])) % 4096
}

func (l *attemptLimits) allow(ip, email string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	emailIdx := bucket(db.NormalizeEmail(email))
	if l.emails[emailIdx] == nil {
		l.emails[emailIdx] = rate.NewLimiter(rate.Every(12*time.Second), attemptsPerEmailBurst)
	}
	emailRes := l.emails[emailIdx].ReserveN(now, 1)
	if emailRes.DelayFrom(now) > 0 {
		emailRes.CancelAt(now)
		return false
	}
	if ip == "" {
		return true
	}
	ipIdx := bucket(ip)
	if l.ips[ipIdx] == nil {
		l.ips[ipIdx] = rate.NewLimiter(rate.Every(3*time.Second), attemptsPerIPBurst)
	}
	if !l.ips[ipIdx].AllowN(now, 1) {
		emailRes.CancelAt(now)
		return false
	}
	return true
}

// clientIP returns the end-user IP for per-IP limits, or "" to skip them. M2M
// callers such as etu-web proxy many users, so only their x-etu-client-ip is
// trusted. Otherwise the last X-Forwarded-For hop wins: Caddy discards
// client-supplied values and sets it to the connecting address.
func clientIP(ctx context.Context) string {
	if auth.IsM2MAuth(ctx) {
		md, _ := metadata.FromIncomingContext(ctx)
		if values := md.Get(clientIPMetadataKey); len(values) > 0 {
			if addr, err := netip.ParseAddr(strings.TrimSpace(values[0])); err == nil {
				return addr.String()
			}
		}
		return ""
	}
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if values := md.Get("x-forwarded-for"); len(values) > 0 {
			hops := strings.Split(values[len(values)-1], ",")
			if last := strings.TrimSpace(hops[len(hops)-1]); last != "" {
				return last
			}
		}
	}
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		host, _, err := net.SplitHostPort(p.Addr.String())
		if err != nil {
			return p.Addr.String()
		}
		return host
	}
	return ""
}
