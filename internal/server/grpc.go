package server

import (
	"log/slog"

	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	awgv1 "github.com/mrcsin/awg-grpc/gen/awg/v1"
	"github.com/mrcsin/awg-grpc/internal/awg"
)

// NewGRPCServer puts RecoverUnary first in the unary chain.
func NewGRPCServer(runner awg.Runner, lookup Lookup, interfaces []Interface, versions Versions, logger *slog.Logger) *grpc.Server {
	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(RecoverUnary(logger)))
	awgv1.RegisterManagementServiceServer(srv, NewManagement(runner, lookup, interfaces, versions))
	healthpb.RegisterHealthServer(srv, NewHealth(lookup, interfaces))
	return srv
}
