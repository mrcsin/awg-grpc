package node

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mrcsin/awg-grpc/internal/awg"
	"github.com/mrcsin/awg-grpc/internal/awg/awgtest"
)

const interfaceSection = "[Interface]\nPrivateKey = test\nAddress = 10.8.1.1/24\nListenPort = 51820\n"

// writeDir creates a config directory holding files (name to content) and directories.
func writeDir(t *testing.T, files map[string]string, dirs ...string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	for _, d := range dirs {
		if err := os.Mkdir(filepath.Join(dir, d), 0o700); err != nil {
			t.Fatalf("creating %s: %v", d, err)
		}
	}
	return dir
}

func TestDiscover(t *testing.T) {
	tests := []struct {
		name      string
		files     map[string]string
		dirs      []string
		wantNames []string
		wantErr   string
	}{
		{
			name:      "conf files become interfaces in name order",
			files:     map[string]string{"awg1.conf": interfaceSection, "awg0.conf": interfaceSection},
			wantNames: []string{"awg0", "awg1"},
		},
		{
			name: "other files and directories are ignored",
			files: map[string]string{
				"awg0.conf":         interfaceSection,
				"awg0.conf.example": "[Peer]\n",
				"README":            "notes",
				"awg0.conf.swp":     "",
			},
			dirs:      []string{"backup.conf"},
			wantNames: []string{"awg0"},
		},
		{
			name:      "name of 15 characters with every allowed symbol class",
			files:     map[string]string{"a_b=c+d.e-f0123.conf": interfaceSection},
			wantNames: []string{"a_b=c+d.e-f0123"},
		},
		{
			name:      "commented peer header is not a peer section",
			files:     map[string]string{"awg0.conf": interfaceSection + "# [Peer]\n"},
			wantNames: []string{"awg0"},
		},
		{name: "empty directory", wantErr: "no *.conf"},
		{name: "only other files", files: map[string]string{"README": "x"}, wantErr: "no *.conf"},
		{name: "leading dash", files: map[string]string{"-x.conf": interfaceSection}, wantErr: `"-x"`},
		{name: "leading dot", files: map[string]string{".x.conf": interfaceSection}, wantErr: `".x"`},
		{name: "empty name", files: map[string]string{".conf": interfaceSection}, wantErr: `""`},
		{name: "keyword all", files: map[string]string{"all.conf": interfaceSection}, wantErr: `"all"`},
		{name: "keyword interfaces", files: map[string]string{"interfaces.conf": interfaceSection}, wantErr: `"interfaces"`},
		{name: "16 characters", files: map[string]string{"abcdefghijklmnop.conf": interfaceSection}, wantErr: `"abcdefghijklmnop"`},
		{name: "space in name", files: map[string]string{"awg 0.conf": interfaceSection}, wantErr: `"awg 0"`},
		{
			name:    "peer section",
			files:   map[string]string{"awg0.conf": interfaceSection + "[Peer]\nPublicKey = x\n"},
			wantErr: "awg0.conf: [Peer] section on line 5",
		},
		{
			name:    "peer section in lower case with spaces and a comment",
			files:   map[string]string{"awg0.conf": interfaceSection + "  [ peer ]  # users\n"},
			wantErr: "[Peer] section on line 5",
		},
		{
			name:    "one bad file among good ones",
			files:   map[string]string{"awg0.conf": interfaceSection, "awg1.conf": "[Peer]\n"},
			wantErr: "awg1.conf: [Peer] section on line 1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeDir(t, tt.files, tt.dirs...)
			got, err := Discover(dir)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Discover() = %v, want error containing %q", got, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Discover() error = %q, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Discover() error = %v", err)
			}
			var want []Interface
			for _, n := range tt.wantNames {
				want = append(want, Interface{Name: n, Path: filepath.Join(dir, n+".conf")})
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Discover() = %v, want %v", got, want)
			}
		})
	}
}

func TestDiscoverMissingDirectory(t *testing.T) {
	_, err := Discover(filepath.Join(t.TempDir(), "absent"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Discover() error = %v, want os.ErrNotExist", err)
	}
}

func TestDiscoverRelativeDirectoryGivesAbsolutePaths(t *testing.T) {
	dir := writeDir(t, map[string]string{"awg0.conf": interfaceSection})
	t.Chdir(dir)

	got, err := Discover(".")
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(got) != 1 || !filepath.IsAbs(got[0].Path) {
		t.Fatalf("Discover() = %+v, want one interface with an absolute path", got)
	}
	gotInfo, err := os.Stat(got[0].Path)
	if err != nil {
		t.Fatalf("stat %s: %v", got[0].Path, err)
	}
	wantInfo, err := os.Stat(filepath.Join(dir, "awg0.conf"))
	if err != nil {
		t.Fatalf("stat fixture: %v", err)
	}
	if !os.SameFile(gotInfo, wantInfo) {
		t.Errorf("Discover() path %s is not the config file in %s", got[0].Path, dir)
	}
}

func TestDiscoverUnreadableConfigFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink(filepath.Join(dir, "absent"), filepath.Join(dir, "awg0.conf")); err != nil {
		t.Fatalf("creating dangling symlink: %v", err)
	}
	_, err := Discover(dir)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Discover() error = %v, want os.ErrNotExist", err)
	}
}

// stubLookup answers from a fixed table; a name missing from the table is absent.
type stubLookup struct {
	addrs map[string][]netip.Prefix
}

func (s stubLookup) lookup(name string) ([]netip.Prefix, bool, error) {
	addrs, ok := s.addrs[name]
	return addrs, ok, nil
}

func TestBringUp(t *testing.T) {
	ifaces := []Interface{
		{Name: "awg0", Path: "/etc/amnezia/amneziawg/awg0.conf"},
		{Name: "awg1", Path: "/etc/amnezia/amneziawg/awg1.conf"},
	}
	upArgs := func(i int) []string { return []string{"up", ifaces[i].Path} }
	subnet := []netip.Prefix{netip.MustParsePrefix("10.8.1.1/24")}
	errTool := &awg.ExitError{Code: 1, Stderr: "RTNETLINK answers: File exists"}

	tests := []struct {
		name      string
		respond   func(r *awgtest.Runner)
		lookup    stubLookup
		wantCalls int
		wantErr   []string
		wantAs    bool
	}{
		{
			name: "every file comes up and has an address",
			respond: func(r *awgtest.Runner) {
				r.Respond("awg-quick", upArgs(0), nil, nil)
				r.Respond("awg-quick", upArgs(1), nil, nil)
			},
			lookup:    stubLookup{addrs: map[string][]netip.Prefix{"awg0": subnet, "awg1": subnet}},
			wantCalls: 2,
		},
		{
			name: "failure names the interface and stops the bring-up",
			respond: func(r *awgtest.Runner) {
				r.Respond("awg-quick", upArgs(0), nil, errTool)
				r.Respond("awg-quick", upArgs(1), nil, nil)
			},
			lookup:    stubLookup{addrs: map[string][]netip.Prefix{"awg0": subnet, "awg1": subnet}},
			wantCalls: 1,
			wantErr:   []string{"awg0", "File exists"},
			wantAs:    true,
		},
		{
			name: "interface without addresses",
			respond: func(r *awgtest.Runner) {
				r.Respond("awg-quick", upArgs(0), nil, nil)
				r.Respond("awg-quick", upArgs(1), nil, nil)
			},
			lookup:    stubLookup{addrs: map[string][]netip.Prefix{"awg0": subnet, "awg1": {}}},
			wantCalls: 2,
			wantErr:   []string{"awg1", "no address"},
		},
		{
			name: "interface absent after bring-up",
			respond: func(r *awgtest.Runner) {
				r.Respond("awg-quick", upArgs(0), nil, nil)
				r.Respond("awg-quick", upArgs(1), nil, nil)
			},
			lookup:    stubLookup{addrs: map[string][]netip.Prefix{"awg0": subnet}},
			wantCalls: 2,
			wantErr:   []string{"awg1", "absent"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := awgtest.NewRunner()
			tt.respond(runner)
			err := BringUp(context.Background(), runner, tt.lookup.lookup, ifaces)

			calls := runner.Calls()
			if len(calls) != tt.wantCalls {
				t.Fatalf("runner calls = %v, want %d", calls, tt.wantCalls)
			}
			for i, c := range calls {
				want := awg.Command{Name: "awg-quick", Args: upArgs(i)}
				if !reflect.DeepEqual(c, want) {
					t.Fatalf("call %d = %v, want %v", i, c, want)
				}
			}
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("BringUp() error = %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("BringUp() error = nil, want %v", tt.wantErr)
			}
			for _, s := range tt.wantErr {
				if !strings.Contains(err.Error(), s) {
					t.Fatalf("BringUp() error = %q, want it to contain %q", err, s)
				}
			}
			var exitErr *awg.ExitError
			if tt.wantAs && !errors.As(err, &exitErr) {
				t.Fatalf("BringUp() error = %v, want it to wrap *awg.ExitError", err)
			}
		})
	}
}
