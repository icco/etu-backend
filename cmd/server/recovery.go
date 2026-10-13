package main

import (
	"context"
	"runtime/debug"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/recovery"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// recoveryInterceptors returns unary and stream interceptors that convert
// handler panics into codes.Internal after logging the panic and stack.
func recoveryInterceptors(log *zap.SugaredLogger) (grpc.UnaryServerInterceptor, grpc.StreamServerInterceptor) {
	opt := recovery.WithRecoveryHandlerContext(func(_ context.Context, p any) error {
		log.Errorw("recovered from panic", "panic", p, "stack", string(debug.Stack()))
		return status.Error(codes.Internal, "internal error")
	})
	return recovery.UnaryServerInterceptor(opt), recovery.StreamServerInterceptor(opt)
}
