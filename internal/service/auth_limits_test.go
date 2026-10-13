package service

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	pb "go.icco.me/etu-backend/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

func TestAttemptLimits(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name string
		ips  func(i int) string
		mail func(i int) string
		want int
	}{
		{name: "per email", ips: func(i int) string { return "10.0.0." + string(rune('a'+i)) }, mail: func(int) string { return "alice@example.com" }, want: attemptsPerEmailBurst},
		{name: "per ip", ips: func(int) string { return "10.0.0.1" }, mail: func(i int) string { return string(rune('a'+i)) + "@example.com" }, want: attemptsPerIPBurst},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := newAttemptLimits()
			allowed := 0
			for i := range 26 {
				if l.allow(tc.ips(i), tc.mail(i), now) {
					allowed++
				}
			}
			if diff := cmp.Diff(tc.want, allowed); diff != "" {
				t.Errorf("allowed (-want +got):\n%s", diff)
			}
		})
	}
}

func TestAttemptLimitsNormalizesEmail(t *testing.T) {
	l := newAttemptLimits()
	now := time.Now()
	for range attemptsPerEmailBurst {
		l.allow("1.1.1.1", "Bob@Example.com", now)
	}
	if l.allow("2.2.2.2", " bob@example.COM ", now) {
		t.Fatal("email case bypassed limit")
	}
}

func TestClientIP(t *testing.T) {
	addr := &net.TCPAddr{IP: net.ParseIP("192.0.2.7"), Port: 4444}
	for _, tc := range []struct {
		name string
		ctx  context.Context
		want string
	}{
		{name: "none", ctx: context.Background(), want: ""},
		{name: "peer", ctx: peer.NewContext(context.Background(), &peer.Peer{Addr: addr}), want: "192.0.2.7"},
		{name: "proxy appended forwarded for", ctx: metadata.NewIncomingContext(peer.NewContext(context.Background(), &peer.Peer{Addr: addr}), metadata.Pairs("x-forwarded-for", "6.6.6.6, 203.0.113.9")), want: "203.0.113.9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if diff := cmp.Diff(tc.want, clientIP(tc.ctx)); diff != "" {
				t.Errorf("clientIP (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRegisterAndAuthenticateRateLimited(t *testing.T) {
	s := NewAuthService(nil)
	for range attemptsPerEmailBurst {
		s.attemptLimits.allow("", testEmail, time.Now())
	}
	if _, err := s.Register(context.Background(), &pb.RegisterRequest{Email: testEmail, Password: testPassword}); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("Register code = %v", status.Code(err))
	}
	if _, err := s.Authenticate(context.Background(), &pb.AuthenticateRequest{Email: testEmail, Password: testPassword}); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("Authenticate code = %v", status.Code(err))
	}
}
