package node

import (
	"context"
	"net/netip"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mrcsin/awg-grpc/internal/awg"
	"github.com/mrcsin/awg-grpc/internal/awg/awgtest"
)

func TestStart(t *testing.T) {
	const versionOutput = "amneziawg-tools v3.1.20260812 - https://amnezia.org\n"
	versionArgs := []string{"--version"}
	exitErr := &awg.ExitError{Code: 1, Stderr: "awg: not found"}

	tests := []struct {
		name      string
		respond   func(r *awgtest.Runner)
		wantCalls []string
		wantErr   []string
	}{
		{
			name: "every step passes",
			respond: func(r *awgtest.Runner) {
				r.Respond("awg", versionArgs, []byte(versionOutput), nil)
			},
			wantCalls: []string{"awg-quick up", "awg-quick up", "awg --version", "awg show", "awg set"},
		},
		{
			name: "tools version fails to run",
			respond: func(r *awgtest.Runner) {
				r.Respond("awg", versionArgs, nil, exitErr)
			},
			wantCalls: []string{"awg-quick up", "awg-quick up", "awg --version"},
			wantErr:   []string{"reading tools version", "awg: not found"},
		},
		{
			name: "tools version does not parse",
			respond: func(r *awgtest.Runner) {
				r.Respond("awg", versionArgs, []byte("wireguard-tools v1.0.20210914\n"), nil)
			},
			wantCalls: []string{"awg-quick up", "awg-quick up", "awg --version"},
			wantErr:   []string{"parsing tools version", "wireguard-tools v1.0.20210914"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeDir(t, map[string]string{"awg1.conf": interfaceSection, "awg0.conf": interfaceSection})
			runner := awgtest.NewRunner()
			for _, name := range []string{"awg0", "awg1"} {
				runner.Respond("awg-quick", []string{"up", filepath.Join(dir, name+".conf")}, nil, nil)
			}
			runner.Respond("awg", []string{"show", "awg0", "disable-cookies"}, []byte("off\n"), nil)
			runner.Respond("awg", []string{"set", "awg0", "disable-cookies", "off"}, nil, nil)
			tt.respond(runner)
			lookup := stubLookup{addrs: map[string][]netip.Prefix{
				"awg0": {netip.MustParsePrefix("10.8.1.1/24")},
				"awg1": {netip.MustParsePrefix("10.8.2.1/24")},
			}}

			names, toolsVersion, err := Start(context.Background(), runner, lookup.lookup, dir)

			var calls []string
			for _, c := range runner.Calls() {
				calls = append(calls, c.Name+" "+c.Args[0])
			}
			if !slices.Equal(calls, tt.wantCalls) {
				t.Errorf("calls = %q, want %q", calls, tt.wantCalls)
			}
			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("Start() error = nil, want %q", tt.wantErr)
				}
				for _, s := range tt.wantErr {
					if !strings.Contains(err.Error(), s) {
						t.Errorf("Start() error = %q, want it to contain %q", err, s)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("Start() error = %v", err)
			}
			if want := []string{"awg0", "awg1"}; !slices.Equal(names, want) {
				t.Errorf("names = %q, want %q", names, want)
			}
			if toolsVersion != "3.1.20260812" {
				t.Errorf("tools version = %q, want 3.1.20260812", toolsVersion)
			}
		})
	}
}
