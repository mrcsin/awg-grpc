package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestDispatch(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		env          map[string]string
		ignoreCode   bool
		wantCode     int
		wantStderr   []string
		wantNoStderr []string
	}{
		{name: "no subcommand", args: nil, wantCode: 2, wantStderr: []string{"usage: awg-grpc serve|healthcheck"}},
		{name: "unknown subcommand", args: []string{"status"}, wantCode: 2, wantStderr: []string{"usage:"}},
		{name: "extra argument", args: []string{"healthcheck", "now"}, wantCode: 2, wantStderr: []string{"usage:"}},
		{
			name:       "bad environment stops serve before startup",
			args:       []string{"serve"},
			env:        map[string]string{"AWG_GRPC_SOCKET_GID": "users"},
			wantCode:   1,
			wantStderr: []string{"level=ERROR", `msg="reading configuration"`, "AWG_GRPC_SOCKET_GID"},
		},
		{
			name:         "healthcheck does not read the environment",
			args:         []string{"healthcheck"},
			env:          map[string]string{"AWG_GRPC_SOCKET_GID": "users"},
			ignoreCode:   true,
			wantNoStderr: []string{"reading configuration", "AWG_GRPC_SOCKET_GID"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			code := dispatch(tt.args, func(key string) string { return tt.env[key] }, &stderr)
			if !tt.ignoreCode && code != tt.wantCode {
				t.Errorf("dispatch() = %d, want %d", code, tt.wantCode)
			}
			for _, want := range tt.wantStderr {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
				}
			}
			for _, unwanted := range tt.wantNoStderr {
				if strings.Contains(stderr.String(), unwanted) {
					t.Errorf("stderr = %q, want no %q", stderr.String(), unwanted)
				}
			}
		})
	}
}
