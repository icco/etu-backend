package main

import (
	"context"
	"time"

	"go.icco.me/etu-backend/internal/auth"
	"go.icco.me/etu-backend/internal/service"
	pb "go.icco.me/etu-backend/proto"
	"go.icco.me/gutil/logging"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const globalStatsMethod = "/etu.StatsService/GetStats"

var m2mPaywallBypassMethods = map[string]bool{
	"/etu.AuthService/Register":                  true,
	"/etu.AuthService/Authenticate":              true,
	"/etu.AuthService/Login":                     true,
	"/etu.AuthService/GetUser":                   true,
	"/etu.AuthService/GetUserByStripeCustomerId": true,
	"/etu.AuthService/UpdateUserSubscription":    true,
	"/etu.AuthService/UpdateUserStripeCustomer":  true,
	"/etu.AccountService/DeleteAccount":          true,
	"/etu.AccountService/ExportData":             true,
}

type accountStateGetter interface {
	GetAccountState(ctx context.Context, userID string) (auth.AccountState, error)
}

type userIDGetter interface {
	GetUserId() string
}

// paywallInterceptor must run after authInterceptor so the caller identity is in ctx.
func paywallInterceptor(accounts accountStateGetter, paywall auth.Paywall) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if err := enforcePaywall(ctx, info.FullMethod, req, accounts, paywall); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// paywallStreamInterceptor must run after stream authentication so the caller identity is in the stream context.
func paywallStreamInterceptor(accounts accountStateGetter, paywall auth.Paywall) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := ss.Context()
		if auth.IsM2MAuth(ctx) && !m2mPaywallBypassMethods[info.FullMethod] {
			return handler(srv, &paywallServerStream{ServerStream: ss, method: info.FullMethod, accounts: accounts, paywall: paywall})
		}
		if err := enforcePaywall(ctx, info.FullMethod, nil, accounts, paywall); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}

// paywallServerStream defers the M2M check to the first received message, which carries the user_id.
type paywallServerStream struct {
	grpc.ServerStream
	method   string
	accounts accountStateGetter
	paywall  auth.Paywall
	checked  bool
}

func (s *paywallServerStream) RecvMsg(m interface{}) error {
	if err := s.ServerStream.RecvMsg(m); err != nil {
		return err
	}
	if s.checked {
		return nil
	}
	if err := enforcePaywall(s.Context(), s.method, m, s.accounts, s.paywall); err != nil {
		return err
	}
	s.checked = true
	return nil
}

func (s *paywallServerStream) SendMsg(m interface{}) error {
	if !s.checked {
		return status.Error(codes.PermissionDenied, "subscription check required before sending")
	}
	return s.ServerStream.SendMsg(m)
}

func enforcePaywall(ctx context.Context, method string, req interface{}, accounts accountStateGetter, paywall auth.Paywall) error {
	if publicMethods[method] {
		return nil
	}
	var userID string
	if auth.IsM2MAuth(ctx) {
		if m2mPaywallBypassMethods[method] {
			return nil
		}
		userID = m2mRequestUserID(req)
		if userID == "" {
			if method == globalStatsMethod && req != nil {
				return nil
			}
			return status.Error(codes.InvalidArgument, "user_id is required")
		}
	} else {
		id, err := auth.GetUserID(ctx)
		if err != nil {
			return status.Error(codes.Unauthenticated, "missing authentication")
		}
		userID = id
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

func m2mRequestUserID(req interface{}) string {
	if g, ok := req.(userIDGetter); ok && g.GetUserId() != "" {
		return g.GetUserId()
	}
	if r, ok := req.(*pb.GetProfileImageURLRequest); ok {
		owner, _ := service.ProfileImageKeyOwner(r.GetKey())
		return owner
	}
	return ""
}
