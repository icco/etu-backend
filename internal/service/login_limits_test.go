package service

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/icco/etu-backend/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestLoginLimitsAccountAndRefill(t *testing.T) {
	l := newLoginLimits()
	now := time.Now()
	for i := 0; i < 5; i++ {
		if !l.acquire("Test@example.com", now) {
			t.Fatal("burst denied")
		}
		l.release()
	}
	if l.acquire(" test@EXAMPLE.com ", now) {
		t.Fatal("normalized account bypassed limit")
	}
	if !l.acquire("test@example.com", now.Add(12*time.Second)) {
		t.Fatal("quota did not refill")
	}
	l.release()
}

func TestLoginLimitsGlobal(t *testing.T) {
	l := newLoginLimits()
	now := time.Now()
	allowed := 0
	for i := 0; i < 1000; i++ {
		if l.acquire(fmt.Sprintf("user%d@example.com", i), now) {
			allowed++
			l.release()
		}
	}
	if allowed != 10 {
		t.Fatalf("allowed %d requests, want 10", allowed)
	}
	if !l.acquire("another@example.com", now.Add(time.Second)) {
		t.Fatal("global quota did not refill")
	}
	l.release()
}

func TestLoginLimitsConcurrent(t *testing.T) {
	l := newLoginLimits()
	var allowed atomic.Int32
	var wg sync.WaitGroup
	now := time.Now()
	for i := 0; i < 100; i++ {
		wg.Go(func() {
			if l.acquire("test@example.com", now) {
				allowed.Add(1)
			}
		})
	}
	wg.Wait()
	if allowed.Load() != 4 {
		t.Fatalf("in-flight calls = %d, want 4", allowed.Load())
	}
	for range allowed.Load() {
		l.release()
	}
	if !l.acquire("test@example.com", now.Add(time.Minute)) {
		t.Fatal("in-flight slots were not released")
	}
	l.release()
}

func TestLoginThrottledBeforeDatabaseAccess(t *testing.T) {
	s := NewAuthService(nil) // Any database access would panic.
	now := time.Now()
	for i := 0; i < 5; i++ {
		if !s.loginLimits.acquire(testEmail, now) {
			t.Fatal("burst denied")
		}
		s.loginLimits.release()
	}
	_, err := s.Login(context.Background(), &pb.AuthenticateRequest{Email: testEmail, Password: testPassword})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("got %v", err)
	}
}
