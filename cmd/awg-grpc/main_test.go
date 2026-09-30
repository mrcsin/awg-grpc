package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestDispatch(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		env        map[string]string
		wantCode   int
		wantStderr []string
	}{
		{name: "no subcommand", args: nil, wantCode: 2, wantStderr: []string{"usage: awg-grpc serve|healthcheck"}},
		{name: "unknown subcommand", args: []string{"status"}, wantCode: 2, wantStderr: []string{"usage:"}},
		{name: "extra argument", args: []string{"healthcheck", "now"}, wantCode: 2, wantStderr: []string{"usage:"}},
		{
			name:       "bad environment is logged as slog text and stops before the subcommand",
			args:       []string{"serve"},
			env:        map[string]string{"AWG_GRPC_SOCKET_GID": "users"},
			wantCode:   1,
			wantStderr: []string{"level=ERROR", `msg="reading configuration"`, "AWG_GRPC_SOCKET_GID"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			code := dispatch(tt.args, func(key string) string { return tt.env[key] }, &stderr)
			if code != tt.wantCode {
				t.Errorf("dispatch() = %d, want %d", code, tt.wantCode)
			}
			for _, want := range tt.wantStderr {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
				}
			}
		})
	}
}

func TestDispatchHealthcheckReadsSocketFromEnvironment(t *testing.T) {
	path := socketPath(t)
	serveHealthOnSocket(t, path, presence(true))
	env := map[string]string{"AWG_GRPC_SOCKET": path}
	var stderr bytes.Buffer
	if code := dispatch([]string{"healthcheck"}, func(key string) string { return env[key] }, &stderr); code != 0 {
		t.Errorf("dispatch(healthcheck) = %d, want 0; stderr %q", code, stderr.String())
	}
}
