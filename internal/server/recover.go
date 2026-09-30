package server

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RecoverUnary returns the unary interceptor that turns a handler panic into INTERNAL. The
// process is the main process of the container whose network namespace holds every interface, so
// a panic must never end it. It logs the method, the panic value and the stack, never the request.
func RecoverUnary(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("handler panicked",
					"method", info.FullMethod,
					"panic", fmt.Sprint(r),
					"stack", string(debug.Stack()))
				resp, err = nil, status.Error(codes.Internal, "internal error")
			}
		}()
		return handler(ctx, req)
	}
}
