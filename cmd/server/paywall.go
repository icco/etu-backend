package main

import (
	"context"
	"time"

	"go.icco.me/etu-backend/internal/auth"
	"go.icco.me/gutil/logging"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type accountStateGetter interface {
	GetAccountState(ctx context.Context, userID string) (auth.AccountState, error)
}

// paywallInterceptor must run after authInterceptor so the caller identity is in ctx.
func paywallInterceptor(accounts accountStateGetter, paywall auth.Paywall) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if err := enforcePaywall(ctx, info.FullMethod, accounts, paywall); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// paywallStreamInterceptor must run after stream authentication so the caller identity is in the stream context.
func paywallStreamInterceptor(accounts accountStateGetter, paywall auth.Paywall) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := enforcePaywall(ss.Context(), info.FullMethod, accounts, paywall); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}

func enforcePaywall(ctx context.Context, method string, accounts accountStateGetter, paywall auth.Paywall) error {
	if publicMethods[method] || auth.IsM2MAuth(ctx) {
		return nil
	}
	userID, err := auth.GetUserID(ctx)
	if err != nil {
		return status.Error(codes.Unauthenticated, "missing authentication")
	}
	l := logging.FromContext(ctx)
	state, err := accounts.GetAccountState(ctx, userID)
	if err != nil {
		l.Errorw("failed to load account state", "method", method, "user_id", userID, zap.Error(err))
		return status.Error(codes.Internal, "failed to load account state")
	}
	if err := paywall.Authorize(method, state, time.Now()); err != nil {
		l.Infow("paywall denied request", "method", method, "user_id", userID, "subscription_status", state.Status, "disabled", state.Disabled)
		return err
	}
	return nil
}
