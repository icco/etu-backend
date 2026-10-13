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

	pb "go.icco.me/etu-backend/proto"
	"google.golang.org/protobuf/proto"
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
		{"m2m billing bypass", "/etu.AuthService/GetUserByStripeCustomerId", auth.SetAuthContext(context.Background(), "m2m", "m2m"), &fakeAccounts{}, codes.OK, false},
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

type fakeServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s fakeServerStream) Context() context.Context { return s.ctx }

func TestPaywallStreamInterceptor(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		ctx      context.Context
		accounts *fakeAccounts
		want     codes.Code
	}{
		{"m2m bypass", "/etu.AccountService/ExportData", auth.SetAuthContext(context.Background(), "m2m", "m2m"), &fakeAccounts{}, codes.OK},
		{"canceled export allowed", "/etu.AccountService/ExportData", auth.SetAuthContext(context.Background(), "u1", "apikey"), &fakeAccounts{state: auth.AccountState{Status: "canceled"}}, codes.OK},
		{"canceled other stream denied", "/etu.NotesService/StreamNotes", auth.SetAuthContext(context.Background(), "u1", "apikey"), &fakeAccounts{state: auth.AccountState{Status: "canceled"}}, codes.PermissionDenied},
		{"disabled export denied", "/etu.AccountService/ExportData", auth.SetAuthContext(context.Background(), "u1", "apikey"), &fakeAccounts{state: auth.AccountState{Status: "active", Disabled: true}}, codes.PermissionDenied},
		{"unauthenticated stream denied", "/etu.AccountService/ExportData", context.Background(), &fakeAccounts{}, codes.Unauthenticated},
		{"lookup error", "/etu.AccountService/ExportData", auth.SetAuthContext(context.Background(), "u1", "apikey"), &fakeAccounts{err: errors.New("boom")}, codes.Internal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			interceptor := paywallStreamInterceptor(tt.accounts, auth.Paywall{Grace: time.Hour})
			err := interceptor(nil, fakeServerStream{ctx: tt.ctx}, &grpc.StreamServerInfo{FullMethod: tt.method, IsServerStream: true}, func(interface{}, grpc.ServerStream) error {
				called = true
				return nil
			})
			if diff := cmp.Diff(tt.want, status.Code(err)); diff != "" {
				t.Errorf("code mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.want == codes.OK, called); diff != "" {
				t.Errorf("handler called mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestPaywallInterceptorM2M(t *testing.T) {
	m2m := auth.SetAuthContext(context.Background(), "m2m", "m2m")
	active := auth.AccountState{Status: "active"}
	canceled := auth.AccountState{Status: "canceled"}
	disabled := auth.AccountState{Status: "active", Disabled: true}
	tests := []struct {
		name       string
		method     string
		req        interface{}
		state      auth.AccountState
		want       codes.Code
		wantLookup bool
	}{
		{"notes active", "/etu.NotesService/CreateNote", &pb.CreateNoteRequest{UserId: "u1"}, active, codes.OK, true},
		{"notes canceled", "/etu.NotesService/CreateNote", &pb.CreateNoteRequest{UserId: "u1"}, canceled, codes.PermissionDenied, true},
		{"notes disabled", "/etu.NotesService/ListNotes", &pb.ListNotesRequest{UserId: "u1"}, disabled, codes.PermissionDenied, true},
		{"notes missing user", "/etu.NotesService/ListNotes", &pb.ListNotesRequest{}, active, codes.InvalidArgument, false},
		{"tags canceled", "/etu.TagsService/ListTags", &pb.ListTagsRequest{UserId: "u1"}, canceled, codes.PermissionDenied, true},
		{"user stats canceled", "/etu.StatsService/GetStats", &pb.GetStatsRequest{UserId: "u1"}, canceled, codes.PermissionDenied, true},
		{"global stats", "/etu.StatsService/GetStats", &pb.GetStatsRequest{}, disabled, codes.OK, false},
		{"settings read canceled", "/etu.UserSettingsService/GetUserSettings", &pb.GetUserSettingsRequest{UserId: "u1"}, canceled, codes.OK, true},
		{"settings read disabled", "/etu.UserSettingsService/GetUserSettings", &pb.GetUserSettingsRequest{UserId: "u1"}, disabled, codes.PermissionDenied, true},
		{"settings write canceled", "/etu.UserSettingsService/UpdateUserSettings", &pb.UpdateUserSettingsRequest{UserId: "u1"}, canceled, codes.PermissionDenied, true},
		{"create key canceled", "/etu.ApiKeysService/CreateApiKey", &pb.CreateApiKeyRequest{UserId: "u1"}, canceled, codes.PermissionDenied, true},
		{"delete key disabled", "/etu.ApiKeysService/DeleteApiKey", &pb.DeleteApiKeyRequest{UserId: "u1"}, disabled, codes.PermissionDenied, true},
		{"profile image bypass", "/etu.UserSettingsService/GetProfileImageURL", &pb.GetProfileImageURLRequest{Key: "k"}, disabled, codes.OK, false},
		{"get user bypass", "/etu.AuthService/GetUser", &pb.GetUserRequest{UserId: "u1"}, disabled, codes.OK, false},
		{"subscription bypass", "/etu.AuthService/UpdateUserSubscription", &pb.UpdateUserSubscriptionRequest{UserId: "u1"}, disabled, codes.OK, false},
		{"stripe customer bypass", "/etu.AuthService/UpdateUserStripeCustomer", &pb.UpdateUserStripeCustomerRequest{UserId: "u1"}, disabled, codes.OK, false},
		{"register bypass", "/etu.AuthService/Register", &pb.RegisterRequest{}, disabled, codes.OK, false},
		{"delete account bypass", "/etu.AccountService/DeleteAccount", nil, disabled, codes.OK, false},
		{"unknown method without user id", "/etu.MediaService/Upload", &pb.GetProfileImageURLRequest{}, active, codes.InvalidArgument, false},
		{"unknown method nil request", "/etu.MediaService/Upload", nil, active, codes.InvalidArgument, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			accounts := &fakeAccounts{state: tt.state}
			interceptor := paywallInterceptor(accounts, auth.Paywall{Grace: time.Hour})
			_, err := interceptor(m2m, tt.req, &grpc.UnaryServerInfo{FullMethod: tt.method}, func(context.Context, interface{}) (interface{}, error) {
				return nil, nil
			})
			if diff := cmp.Diff(tt.want, status.Code(err)); diff != "" {
				t.Errorf("code mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantLookup, accounts.calls > 0); diff != "" {
				t.Errorf("lookup mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

type recvServerStream struct {
	fakeServerStream
	msg *pb.ListNotesRequest
}

func (s recvServerStream) RecvMsg(m interface{}) error {
	proto.Merge(m.(*pb.ListNotesRequest), s.msg)
	return nil
}

func TestPaywallStreamInterceptorM2M(t *testing.T) {
	tests := []struct {
		name   string
		method string
		msg    *pb.ListNotesRequest
		state  auth.AccountState
		want   codes.Code
	}{
		{"export bypass", "/etu.AccountService/ExportData", &pb.ListNotesRequest{}, auth.AccountState{Disabled: true}, codes.OK},
		{"stream active", "/etu.NotesService/StreamNotes", &pb.ListNotesRequest{UserId: "u1"}, auth.AccountState{Status: "active"}, codes.OK},
		{"stream canceled", "/etu.NotesService/StreamNotes", &pb.ListNotesRequest{UserId: "u1"}, auth.AccountState{Status: "canceled"}, codes.PermissionDenied},
		{"stream disabled", "/etu.NotesService/StreamNotes", &pb.ListNotesRequest{UserId: "u1"}, auth.AccountState{Status: "active", Disabled: true}, codes.PermissionDenied},
		{"stream missing user", "/etu.NotesService/StreamNotes", &pb.ListNotesRequest{}, auth.AccountState{Status: "active"}, codes.InvalidArgument},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			interceptor := paywallStreamInterceptor(&fakeAccounts{state: tt.state}, auth.Paywall{Grace: time.Hour})
			stream := recvServerStream{fakeServerStream: fakeServerStream{ctx: auth.SetAuthContext(context.Background(), "m2m", "m2m")}, msg: tt.msg}
			err := interceptor(nil, stream, &grpc.StreamServerInfo{FullMethod: tt.method, IsServerStream: true}, func(_ interface{}, ss grpc.ServerStream) error {
				return ss.RecvMsg(&pb.ListNotesRequest{})
			})
			if diff := cmp.Diff(tt.want, status.Code(err)); diff != "" {
				t.Errorf("code mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
