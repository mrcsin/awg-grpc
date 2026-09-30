package server

import (
	"context"
	"fmt"
	"slices"

	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// Health implements grpc.health.v1.Health/Check for the container: it is SERVING when every
// configured interface is present. The service name in the request is ignored, since the
// container has one health. List and Watch stay unimplemented.
type Health struct {
	healthpb.UnimplementedHealthServer

	lookup Lookup
	names  []string
}

// NewHealth returns the health service over the configured interfaces.
func NewHealth(lookup Lookup, interfaces []string) *Health {
	return &Health{lookup: lookup, names: slices.Clone(interfaces)}
}

// Check runs the presence lookup for every configured interface on each call.
func (h *Health) Check(_ context.Context, _ *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	for _, name := range h.names {
		_, present, err := h.lookup(name)
		if err != nil {
			return nil, toStatus(fmt.Errorf("looking up interface %s: %w", name, err))
		}
		if !present {
			return &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_NOT_SERVING}, nil
		}
	}
	return &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVING}, nil
}
