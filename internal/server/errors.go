package server

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/mrcsin/awg-grpc/internal/reconcile"
)

// toStatus maps a handler error to its gRPC status. Every message is built from error text that
// never holds a preshared key: validation errors omit them and the runner redacts tool stderr.
func toStatus(err error) error {
	if _, ok := status.FromError(err); ok {
		return err
	}
	var invalid *reconcile.ValidationError
	switch {
	case errors.As(err, &invalid):
		return status.Error(codes.InvalidArgument, invalid.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, err.Error())
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}
