package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"go.icco.me/etu-backend/internal/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeAccounts struct {
	state auth.AccountState
	err   error
	calls int
}

func (f *fakeAccounts) GetAccountState(context.Context, string) (auth.AccountState, error) {
	f.calls++
	return f.state, f.err
}

func TestPaywallInterceptor(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		ctx        context.Context
		accounts   *fakeAccounts
		want       codes.Code
		wantLookup bool
	}{
		{"public method", "/etu.AuthService/Login", context.Background(), &fakeAccounts{}, codes.OK, false},
		{"m2m bypass", "/etu.NotesService/ListNotes", auth.SetAuthContext(context.Background(), "m2m", "m2m"), &fakeAccounts{}, codes.OK, false},
		{"active user", "/etu.NotesService/ListNotes", auth.SetAuthContext(context.Background(), "u1", "apikey"), &fakeAccounts{state: auth.AccountState{Status: "active"}}, codes.OK, true},
		{"canceled user", "/etu.NotesService/ListNotes", auth.SetAuthContext(context.Background(), "u1", "apikey"), &fakeAccounts{state: auth.AccountState{Status: "canceled"}}, codes.PermissionDenied, true},
		{"disabled user", "/etu.AuthService/GetUser", auth.SetAuthContext(context.Background(), "u1", "apikey"), &fakeAccounts{state: auth.AccountState{Status: "active", Disabled: true}}, codes.PermissionDenied, true},
		{"lookup error", "/etu.NotesService/ListNotes", auth.SetAuthContext(context.Background(), "u1", "apikey"), &fakeAccounts{err: errors.New("boom")}, codes.Internal, true},
		{"no auth context on private method", "/etu.NotesService/ListNotes", context.Background(), &fakeAccounts{}, codes.Unauthenticated, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			interceptor := paywallInterceptor(tt.accounts, auth.Paywall{Grace: time.Hour})
			_, err := interceptor(tt.ctx, nil, &grpc.UnaryServerInfo{FullMethod: tt.method}, func(context.Context, interface{}) (interface{}, error) {
				return nil, nil
			})
			if diff := cmp.Diff(tt.want, status.Code(err)); diff != "" {
				t.Errorf("code mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantLookup, tt.accounts.calls > 0); diff != "" {
				t.Errorf("lookup mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
