package main

import (
	"strings"
	"testing"
)

func TestConfigFromEnv(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    config
		wantErr string
	}{
		{
			name: "defaults",
			env:  nil,
			want: config{Socket: "/run/awg-grpc/awg.sock", ConfigDir: "/etc/amnezia/amneziawg", SocketGID: -1},
		},
		{
			name: "empty values keep the defaults",
			env:  map[string]string{"AWG_GRPC_SOCKET": "", "AWG_GRPC_CONFIG_DIR": "", "AWG_GRPC_SOCKET_GID": ""},
			want: config{Socket: "/run/awg-grpc/awg.sock", ConfigDir: "/etc/amnezia/amneziawg", SocketGID: -1},
		},
		{
			name: "overrides",
			env: map[string]string{
				"AWG_GRPC_SOCKET":     "/tmp/s/awg.sock",
				"AWG_GRPC_CONFIG_DIR": "/tmp/conf",
				"AWG_GRPC_SOCKET_GID": "1000",
			},
			want: config{Socket: "/tmp/s/awg.sock", ConfigDir: "/tmp/conf", SocketGID: 1000},
		},
		{
			name: "group 0 is a group, not unset",
			env:  map[string]string{"AWG_GRPC_SOCKET_GID": "0"},
			want: config{Socket: "/run/awg-grpc/awg.sock", ConfigDir: "/etc/amnezia/amneziawg", SocketGID: 0},
		},
		{
			name:    "non-numeric group",
			env:     map[string]string{"AWG_GRPC_SOCKET_GID": "users"},
			wantErr: `AWG_GRPC_SOCKET_GID="users"`,
		},
		{
			name:    "negative group",
			env:     map[string]string{"AWG_GRPC_SOCKET_GID": "-1"},
			wantErr: `AWG_GRPC_SOCKET_GID="-1"`,
		},
		{
			name:    "group above 32 bits",
			env:     map[string]string{"AWG_GRPC_SOCKET_GID": "4294967296"},
			wantErr: `AWG_GRPC_SOCKET_GID="4294967296"`,
		},
		{
			name:    "a newline in the group is quoted",
			env:     map[string]string{"AWG_GRPC_SOCKET_GID": "1\n2"},
			wantErr: `AWG_GRPC_SOCKET_GID="1\n2"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := configFromEnv(func(key string) string { return tt.env[key] })
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("configFromEnv() error = %v, want it to contain %s", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("configFromEnv() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("configFromEnv() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
