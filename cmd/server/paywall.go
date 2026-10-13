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
		if publicMethods[info.FullMethod] || auth.IsM2MAuth(ctx) {
			return handler(ctx, req)
		}
		userID, err := auth.GetUserID(ctx)
		if err != nil {
			return nil, status.Error(codes.Unauthenticated, "missing authentication")
		}
		l := logging.FromContext(ctx)
		state, err := accounts.GetAccountState(ctx, userID)
		if err != nil {
			l.Errorw("failed to load account state", "method", info.FullMethod, "user_id", userID, zap.Error(err))
			return nil, status.Error(codes.Internal, "failed to load account state")
		}
		if err := paywall.Authorize(info.FullMethod, state, time.Now()); err != nil {
			l.Infow("paywall denied request", "method", info.FullMethod, "user_id", userID, "subscription_status", state.Status, "disabled", state.Disabled)
			return nil, err
		}
		return handler(ctx, req)
	}
}
