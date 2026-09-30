//go:build linux

package awg

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const (
	testPSK       = "Tm90QVJlYWxQcmVzaGFyZWRLZXlKdXN0QVRlc3RWYWw="
	testPublicKey = "oqNOS0iTnFF8IggAjfLkjRXvFTAsDFVa896865MU4ys="
)

func TestExecRunnerRun(t *testing.T) {
	tests := []struct {
		name       string
		cmd        Command
		wantStdout string
		wantExit   *ExitError
	}{
		{
			name:       "stdout returned",
			cmd:        Command{Name: "sh", Args: []string{"-c", "printf 'line one\\nline two\\n'"}},
			wantStdout: "line one\nline two\n",
		},
		{
			name:     "non-zero exit returns code and stderr",
			cmd:      Command{Name: "sh", Args: []string{"-c", "echo boom >&2; exit 3"}},
			wantExit: &ExitError{Code: 3, Stderr: "boom\n"},
		},
		{
			name: "extra files readable as /dev/fd/3 and /dev/fd/4",
			cmd: Command{
				Name:       "sh",
				Args:       []string{"-c", "cat /dev/fd/3; cat /dev/fd/4"},
				ExtraFiles: [][]byte{[]byte("first\n"), []byte("second\n")},
			},
			wantStdout: "first\nsecond\n",
		},
		{
			name: "fd content echoed to stderr is redacted",
			cmd: Command{
				Name: "sh",
				Args: []string{"-c",
					"printf 'Key is not the correct length or format: `%s'\"'\"'\\n' \"$(cat /dev/fd/3)\" >&2; " +
						"echo \"peer " + testPublicKey + "\" >&2; exit 1"},
				ExtraFiles: [][]byte{[]byte(testPSK + "\n")},
			},
			wantExit: &ExitError{
				Code:   1,
				Stderr: "Key is not the correct length or format: `***'\npeer " + testPublicKey + "\n",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := ExecRunner{}.Run(context.Background(), tt.cmd)
			if tt.wantExit == nil {
				if err != nil {
					t.Fatalf("Run() error = %v", err)
				}
				if string(out) != tt.wantStdout {
					t.Errorf("Run() stdout = %q, want %q", out, tt.wantStdout)
				}
				return
			}
			var exitErr *ExitError
			if !errors.As(err, &exitErr) {
				t.Fatalf("Run() error = %v, want *ExitError", err)
			}
			if *exitErr != *tt.wantExit {
				t.Errorf("Run() ExitError = %+v, want %+v", *exitErr, *tt.wantExit)
			}
			if strings.Contains(err.Error(), testPSK) {
				t.Errorf("Run() error holds the extra file content: %v", err)
			}
		})
	}
}

func TestExecRunnerMissingBinary(t *testing.T) {
	_, err := ExecRunner{}.Run(context.Background(), Command{Name: "/nonexistent/awg"})
	if err == nil {
		t.Fatal("Run() error = nil, want an error")
	}
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		t.Errorf("Run() error = %v, want a start error, not *ExitError", err)
	}
}

func TestExecRunnerContextEndStopsProcess(t *testing.T) {
	tests := []struct {
		name    string
		ctx     func() (context.Context, context.CancelFunc)
		wantErr error
	}{
		{
			name: "cancel",
			ctx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				time.AfterFunc(100*time.Millisecond, cancel)
				return ctx, cancel
			},
			wantErr: context.Canceled,
		},
		{
			name: "deadline",
			ctx: func() (context.Context, context.CancelFunc) {
				return context.WithTimeout(context.Background(), 100*time.Millisecond)
			},
			wantErr: context.DeadlineExceeded,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := tt.ctx()
			defer cancel()
			start := time.Now()
			_, err := ExecRunner{}.Run(ctx, Command{Name: "sleep", Args: []string{"30"}})
			if elapsed := time.Since(start); elapsed > 10*time.Second {
				t.Errorf("Run() returned after %v, want the process stopped", elapsed)
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Run() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
