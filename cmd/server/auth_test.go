package main

import (
	"context"
	"testing"

	"github.com/icco/etu-backend/internal/auth"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
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
