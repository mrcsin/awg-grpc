package server

import (
	"context"
	"io"
	"testing"

	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func serveHealth(t *testing.T, h *Health) healthpb.HealthClient {
	t.Helper()
	conn := serveBufconn(t, io.Discard, func(srv *grpc.Server) { healthpb.RegisterHealthServer(srv, h) })
	return healthpb.NewHealthClient(conn)
}

func TestHealthCheck(t *testing.T) {
	configured := []string{presentIface, absentIface}
	bothPresent := lookupOf(map[string]string{presentIface: "10.8.1.1/24", absentIface: "10.8.2.1/24"})
	tests := []struct {
		name    string
		lookup  Lookup
		service string
		want    healthpb.HealthCheckResponse_ServingStatus
	}{
		{
			name:   "every interface present is SERVING",
			lookup: bothPresent,
			want:   healthpb.HealthCheckResponse_SERVING,
		},
		{
			name:   "one interface absent is NOT_SERVING",
			lookup: awg0Lookup,
			want:   healthpb.HealthCheckResponse_NOT_SERVING,
		},
		{
			name:   "every interface absent is NOT_SERVING",
			lookup: lookupOf(nil),
			want:   healthpb.HealthCheckResponse_NOT_SERVING,
		},
		{
			name:    "the management service name gets the container answer",
			lookup:  bothPresent,
			service: "awg.v1.ManagementService",
			want:    healthpb.HealthCheckResponse_SERVING,
		},
		{
			name:    "an unknown service name gets the container answer",
			lookup:  awg0Lookup,
			service: "no.such.Service",
			want:    healthpb.HealthCheckResponse_NOT_SERVING,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := serveHealth(t, NewHealth(tt.lookup, configured))
			resp, err := client.Check(context.Background(), &healthpb.HealthCheckRequest{Service: tt.service})
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			if resp.GetStatus() != tt.want {
				t.Errorf("status = %s, want %s", resp.GetStatus(), tt.want)
			}
		})
	}
}
