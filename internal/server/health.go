package server

import (
	"context"
	"fmt"
	"slices"

	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// Health is SERVING when every configured interface is present; the request's service name is
// ignored. List and Watch are unimplemented.
type Health struct {
	healthpb.UnimplementedHealthServer

	lookup Lookup
	names  []string
}

func NewHealth(lookup Lookup, interfaces []string) *Health {
	return &Health{lookup: lookup, names: slices.Clone(interfaces)}
}

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
