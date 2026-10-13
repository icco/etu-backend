package main

import (
	"context"
	"testing"

	"go.icco.me/etu-backend/internal/auth"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestLoginPublicButKeyCreationProtected(t *testing.T) {
	interceptor := authInterceptor(nil, &auth.M2MConfig{}, zap.NewNop().Sugar())
	for _, method := range []string{"/etu.AuthService/Login", "/etu.ApiKeysService/CreateApiKey"} {
		called := false
		_, err := interceptor(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: method}, func(context.Context, interface{}) (interface{}, error) {
			called = true
			return nil, nil
		})
		if method == "/etu.AuthService/Login" {
			if err != nil || !called {
				t.Fatal("Login must reach credential validation without a token")
			}
		} else if called || status.Code(err) != codes.Unauthenticated {
			t.Fatal("key creation must still require authentication")
		}
	}
}

type testServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s testServerStream) Context() context.Context { return s.ctx }

func TestStreamAuthInterceptor(t *testing.T) {
	interceptor := streamAuthInterceptor(authInterceptor(nil, &auth.M2MConfig{}, zap.NewNop().Sugar()))
	tests := []struct {
		name       string
		ctx        context.Context
		wantCalled bool
		wantCode   codes.Code
	}{
		{name: "missing metadata is rejected", ctx: context.Background(), wantCode: codes.Unauthenticated},
		{name: "missing authorization is rejected", ctx: metadata.NewIncomingContext(context.Background(), metadata.MD{}), wantCode: codes.Unauthenticated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			err := interceptor(nil, testServerStream{ctx: tt.ctx}, &grpc.StreamServerInfo{FullMethod: "/etu.AccountService/ExportData"}, func(any, grpc.ServerStream) error {
				called = true
				return nil
			})
			if called != tt.wantCalled || status.Code(err) != tt.wantCode {
				t.Fatalf("called=%v code=%v, want called=%v code=%v", called, status.Code(err), tt.wantCalled, tt.wantCode)
			}
		})
	}
}

func TestPublicMethodsRecognizeM2M(t *testing.T) {
	t.Setenv("GRPC_API_KEYS", "m2m-secret")
	interceptor := authInterceptor(nil, auth.NewM2MConfig(context.Background()), zap.NewNop().Sugar())
	for _, tc := range []struct {
		name   string
		token  string
		wantM2 bool
	}{
		{name: "no token", wantM2: false},
		{name: "valid m2m token", token: "m2m-secret", wantM2: true},
		{name: "invalid token stays public", token: "etu_nope", wantM2: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.token != "" {
				ctx = metadata.NewIncomingContext(ctx, metadata.Pairs("authorization", tc.token))
			}
			var got bool
			_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/etu.AuthService/Authenticate"}, func(ctx context.Context, _ interface{}) (interface{}, error) {
				got = auth.IsM2MAuth(ctx)
				return nil, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.wantM2 {
				t.Fatalf("IsM2MAuth = %v, want %v", got, tc.wantM2)
			}
		})
	}
}

func TestStreamAuthInterceptorPassesAuthContext(t *testing.T) {
	unary := func(ctx context.Context, req interface{}, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		return handler(auth.SetAuthContext(ctx, "alice", "apikey"), req)
	}
	var gotUser string
	err := streamAuthInterceptor(unary)(nil, testServerStream{ctx: context.Background()}, &grpc.StreamServerInfo{}, func(_ any, ss grpc.ServerStream) error {
		var err error
		gotUser, err = auth.GetUserID(ss.Context())
		return err
	})
	if err != nil || gotUser != "alice" {
		t.Fatalf("user = %q, err = %v", gotUser, err)
	}
}
