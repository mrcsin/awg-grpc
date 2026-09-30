package node

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/mrcsin/awg-grpc/internal/awg"
	"github.com/mrcsin/awg-grpc/internal/awg/awgtest"
)

func TestProbe(t *testing.T) {
	const toolsVersion = "3.1.20260812"
	showArgs := []string{"show", "awg0", "disable-cookies"}
	setArgs := func(value string) []string { return []string{"set", "awg0", "disable-cookies", value} }
	errSet := &awg.ExitError{Code: 1, Stderr: "Unable to modify interface: Invalid argument"}
	errShow := &awg.ExitError{Code: 1, Stderr: "Unable to access interface: Protocol not supported"}

	tests := []struct {
		name      string
		respond   func(r *awgtest.Runner)
		wantCalls []awg.Command
		wantErr   []string
		wantAs    bool
	}{
		{
			name: "off is set back as off",
			respond: func(r *awgtest.Runner) {
				r.Respond("awg", showArgs, []byte("off\n"), nil)
				r.Respond("awg", setArgs("off"), nil, nil)
			},
			wantCalls: []awg.Command{{Name: "awg", Args: showArgs}, {Name: "awg", Args: setArgs("off")}},
		},
		{
			name: "on is set back as on",
			respond: func(r *awgtest.Runner) {
				r.Respond("awg", showArgs, []byte("on\n"), nil)
				r.Respond("awg", setArgs("on"), nil, nil)
			},
			wantCalls: []awg.Command{{Name: "awg", Args: showArgs}, {Name: "awg", Args: setArgs("on")}},
		},
		{
			name: "set failure is a generation mismatch",
			respond: func(r *awgtest.Runner) {
				r.Respond("awg", showArgs, []byte("off\n"), nil)
				r.Respond("awg", setArgs("off"), nil, errSet)
			},
			wantCalls: []awg.Command{{Name: "awg", Args: showArgs}, {Name: "awg", Args: setArgs("off")}},
			wantErr:   []string{"generation mismatch", "amneziawg-tools v" + toolsVersion, "Invalid argument"},
			wantAs:    true,
		},
		{
			name: "show failure is a generation mismatch",
			respond: func(r *awgtest.Runner) {
				r.Respond("awg", showArgs, nil, errShow)
			},
			wantCalls: []awg.Command{{Name: "awg", Args: showArgs}},
			wantErr:   []string{"generation mismatch", "amneziawg-tools v" + toolsVersion, "Protocol not supported"},
			wantAs:    true,
		},
		{
			name: "unexpected value is a generation mismatch without a set",
			respond: func(r *awgtest.Runner) {
				r.Respond("awg", showArgs, []byte("maybe\n"), nil)
			},
			wantCalls: []awg.Command{{Name: "awg", Args: showArgs}},
			wantErr:   []string{"generation mismatch", "amneziawg-tools v" + toolsVersion, `"maybe"`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := awgtest.NewRunner()
			tt.respond(runner)
			err := Probe(context.Background(), runner, "awg0", toolsVersion)

			if calls := runner.Calls(); !reflect.DeepEqual(calls, tt.wantCalls) {
				t.Fatalf("runner calls = %v, want %v", calls, tt.wantCalls)
			}
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("Probe() error = %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Probe() error = nil, want %v", tt.wantErr)
			}
			for _, s := range tt.wantErr {
				if !strings.Contains(err.Error(), s) {
					t.Fatalf("Probe() error = %q, want it to contain %q", err, s)
				}
			}
			var exitErr *awg.ExitError
			if tt.wantAs && !errors.As(err, &exitErr) {
				t.Fatalf("Probe() error = %v, want it to wrap *awg.ExitError", err)
			}
		})
	}
}
