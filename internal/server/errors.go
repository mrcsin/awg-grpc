package server

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/mrcsin/awg-grpc/internal/reconcile"
)

// unknownInterfaceError reports a request name that no config file defines. The name is
// request input, so it is quoted.
type unknownInterfaceError struct {
	name string
}

func (e *unknownInterfaceError) Error() string {
	return fmt.Sprintf("interface %q is not configured", e.name)
}

// absentInterfaceError reports a configured interface missing from the kernel.
type absentInterfaceError struct {
	name string
}

func (e *absentInterfaceError) Error() string {
	return fmt.Sprintf("interface %s is configured but missing from the kernel", e.name)
}

// toStatus maps a handler error to its gRPC status. Every message is built from error text that
// never holds a preshared key: validation errors omit them and the runner redacts tool stderr.
func toStatus(err error) error {
	var (
		unknown *unknownInterfaceError
		absent  *absentInterfaceError
		invalid *reconcile.ValidationError
	)
	switch {
	case errors.As(err, &unknown):
		return status.Error(codes.NotFound, unknown.Error())
	case errors.As(err, &absent):
		return status.Error(codes.FailedPrecondition, absent.Error())
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
