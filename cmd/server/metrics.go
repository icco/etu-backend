package main

import (
	"fmt"

	grpcprom "github.com/grpc-ecosystem/go-grpc-middleware/providers/prometheus"
	"github.com/prometheus/client_golang/prometheus"
	"go.icco.me/etu-backend/internal/auth"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

func newGRPCServer(registry *prometheus.Registry, authenticator *auth.Authenticator, m2m *auth.M2MConfig, paywall auth.Paywall, log *zap.SugaredLogger) (*grpc.Server, error) {
	metrics := grpcprom.NewServerMetrics(grpcprom.WithServerHandlingTimeHistogram())
	if err := registry.Register(metrics); err != nil {
		return nil, fmt.Errorf("register gRPC metrics: %w", err)
	}
	unaryAuth := authInterceptor(authenticator, m2m, log)
	recoverUnary, recoverStream := recoveryInterceptors(log)
	return grpc.NewServer(
		grpc.ChainUnaryInterceptor(metrics.UnaryServerInterceptor(), recoverUnary, unaryAuth, paywallInterceptor(authenticator, paywall)),
		grpc.ChainStreamInterceptor(metrics.StreamServerInterceptor(), recoverStream, streamAuthInterceptor(unaryAuth), paywallStreamInterceptor(authenticator, paywall)),
		grpc.MaxRecvMsgSize(maxRecvMsgSize),
	), nil
}
