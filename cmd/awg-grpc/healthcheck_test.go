package main

import (
	"bytes"
	"context"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/mrcsin/awg-grpc/internal/server"
)

// socketPath returns a socket path in a fresh short directory. t.TempDir paths on macOS can
// pass the 104-byte sun_path limit.
func socketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "awgh")
	if err != nil {
		t.Fatalf("creating socket directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "awg.sock")
}

func presence(present bool) server.Lookup {
	return func(string) ([]netip.Prefix, bool, error) { return nil, present, nil }
}

func serveHealthOnSocket(t *testing.T, path string, lookup server.Lookup) {
	t.Helper()
	lis, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listening on %s: %v", path, err)
	}
	srv := grpc.NewServer()
	healthpb.RegisterHealthServer(srv, server.NewHealth(lookup, []string{"awg0"}))
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
}

func TestHealthcheck(t *testing.T) {
	tests := []struct {
		name       string
		lookup     server.Lookup // nil: no server listens on the socket
		wantCode   int
		wantStderr string
	}{
		{name: "SERVING exits 0", lookup: presence(true), wantCode: 0},
		{name: "NOT_SERVING exits 1", lookup: presence(false), wantCode: 1, wantStderr: "NOT_SERVING"},
		{name: "absent socket exits 1", lookup: nil, wantCode: 1, wantStderr: "Unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := socketPath(t)
			if tt.lookup != nil {
				serveHealthOnSocket(t, path, tt.lookup)
			}
			var stderr bytes.Buffer
			code := healthcheck(context.Background(), path, &stderr)
			if code != tt.wantCode {
				t.Errorf("healthcheck() = %d, want %d; stderr %q", code, tt.wantCode, stderr.String())
			}
			if tt.wantStderr == "" && stderr.Len() != 0 {
				t.Errorf("stderr = %q, want empty", stderr.String())
			}
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}
