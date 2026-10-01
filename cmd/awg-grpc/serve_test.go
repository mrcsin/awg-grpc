//go:build linux

package main

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	awgv1 "github.com/mrcsin/awg-grpc/gen/awg/v1"
	"github.com/mrcsin/awg-grpc/internal/awg"
	"github.com/mrcsin/awg-grpc/internal/awg/awgtest"
)

const toolsVersionOutput = "amneziawg-tools v3.1.20260812 - https://amnezia.org\n"

type reply struct {
	name   string
	args   []string
	stdout string
	err    error
}

// happyReplies answers every startup command for the interfaces awg0 and awg1 in dir.
func happyReplies(dir string) []reply {
	return []reply{
		{name: "awg-quick", args: []string{"up", filepath.Join(dir, "awg0.conf")}},
		{name: "awg-quick", args: []string{"up", filepath.Join(dir, "awg1.conf")}},
		{name: "awg", args: []string{"--version"}, stdout: toolsVersionOutput},
		{name: "awg", args: []string{"show", "awg0", "disable-cookies"}, stdout: "off\n"},
		{name: "awg", args: []string{"set", "awg0", "disable-cookies", "off"}},
	}
}

// happyEvents is the startup order for happyReplies, with the config directory as $DIR.
var happyEvents = []string{
	"awg-quick up $DIR/awg0.conf",
	"awg-quick up $DIR/awg1.conf",
	"lookup awg0",
	"lookup awg1",
	"awg --version",
	"awg show awg0 disable-cookies",
	"awg set awg0 disable-cookies off",
}

// recorder keeps runner calls and lookups in one sequence, each with whether the socket
// existed at that moment.
type recorder struct {
	mu         sync.Mutex
	dir        string
	socket     string
	events     []string
	socketSeen []bool
}

func (r *recorder) record(event string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, strings.ReplaceAll(event, r.dir, "$DIR"))
	_, err := os.Lstat(r.socket)
	r.socketSeen = append(r.socketSeen, err == nil)
}

func (r *recorder) snapshot() ([]string, []bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.events), slices.Clone(r.socketSeen)
}

type recordingRunner struct {
	fake *awgtest.Runner
	rec  *recorder
	// panicOn is a command line that panics instead of running, as a handler bug would.
	panicOn string
}

func (r recordingRunner) Run(ctx context.Context, cmd awg.Command) ([]byte, error) {
	line := strings.Join(append([]string{cmd.Name}, cmd.Args...), " ")
	r.rec.record(line)
	if line == r.panicOn {
		panic("recordingRunner: " + line)
	}
	return r.fake.Run(ctx, cmd)
}

// startup is one run of the serve startup against a temp config directory and socket.
type startup struct {
	cfg    config
	rec    *recorder
	runner recordingRunner
}

// newStartup writes awg0.conf and awg1.conf into a temp config directory and queues replies;
// edit, when not nil, changes the happy replies first.
func newStartup(t *testing.T, edit func([]reply)) *startup {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"awg0", "awg1"} {
		content := "[Interface]\nListenPort = 51820\n"
		if err := os.WriteFile(filepath.Join(dir, name+".conf"), []byte(content), 0o600); err != nil {
			t.Fatalf("writing config: %v", err)
		}
	}
	replies := happyReplies(dir)
	if edit != nil {
		edit(replies)
	}
	fake := awgtest.NewRunner()
	for _, r := range replies {
		fake.Respond(r.name, r.args, []byte(r.stdout), r.err)
	}
	socket := socketPath(t)
	rec := &recorder{dir: dir, socket: socket}
	return &startup{
		cfg:    config{Socket: socket, ConfigDir: dir, SocketGID: noSocketGID},
		rec:    rec,
		runner: recordingRunner{fake: fake, rec: rec},
	}
}

func (s *startup) lookup(name string) ([]netip.Prefix, bool, error) {
	s.rec.record("lookup " + name)
	return []netip.Prefix{netip.MustParsePrefix("10.8.1.1/24")}, true, nil
}

func (s *startup) start(ctx context.Context, t *testing.T) <-chan error {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(t.Output(), nil))
	done := make(chan error, 1)
	go func() { done <- run(ctx, s.cfg, s.runner, s.lookup, logger) }()
	return done
}

// serving starts run and waits until the health check over the socket passes.
func (s *startup) serving(t *testing.T) (cancel func() error) {
	t.Helper()
	ctx, stop := context.WithCancel(context.Background())
	done := s.start(ctx, t)
	deadline := time.Now().Add(5 * time.Second)
	for healthcheck(context.Background(), s.cfg.Socket, io.Discard) != 0 {
		select {
		case err := <-done:
			stop()
			t.Fatalf("run() returned before serving: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			stop()
			t.Fatal("server did not become healthy within 5s")
		}
		time.Sleep(20 * time.Millisecond)
	}
	var once sync.Once
	var result error
	cancel = func() error {
		once.Do(func() {
			stop()
			select {
			case result = <-done:
			case <-time.After(5 * time.Second):
				result = errors.New("run() did not return within 5s of cancel")
			}
		})
		return result
	}
	t.Cleanup(func() { _ = cancel() })
	return cancel
}

func TestRunStartupOrder(t *testing.T) {
	s := newStartup(t, nil)
	s.serving(t)

	events, socketSeen := s.rec.snapshot()
	if len(events) < len(happyEvents) || !slices.Equal(events[:len(happyEvents)], happyEvents) {
		t.Fatalf("startup events = %q, want prefix %q", events, happyEvents)
	}
	for i := range happyEvents {
		if socketSeen[i] {
			t.Errorf("socket existed at %q; listen must come after the probe", events[i])
		}
	}
}

func TestRunStartupFailures(t *testing.T) {
	exitErr := func(stderr string) error { return &awg.ExitError{Code: 1, Stderr: stderr} }
	tests := []struct {
		name       string
		edit       func([]reply)
		setup      func(t *testing.T, cfg *config)
		wantEvents []string
		wantErr    []string
	}{
		{
			name:       "bring-up failure",
			edit:       func(r []reply) { r[1].err = exitErr("RTNETLINK answers: File exists") },
			wantEvents: happyEvents[:2],
			wantErr:    []string{"awg1", "RTNETLINK answers: File exists"},
		},
		{
			name: "socket directory missing",
			setup: func(t *testing.T, cfg *config) {
				cfg.Socket = filepath.Join(t.TempDir(), "absent", "awg.sock")
			},
			wantEvents: happyEvents,
			wantErr:    []string{"listening", "no such file or directory"},
		},
		{
			name: "socket group the process may not set",
			setup: func(t *testing.T, cfg *config) {
				if os.Geteuid() == 0 {
					t.Skip("root may give the socket any group")
				}
				cfg.SocketGID = foreignGID(t)
			},
			wantEvents: happyEvents,
			wantErr:    []string{"setting socket group", "operation not permitted"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newStartup(t, tt.edit)
			if tt.setup != nil {
				tt.setup(t, &s.cfg)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			var err error
			select {
			case err = <-s.start(ctx, t):
			case <-ctx.Done():
				t.Fatal("run() did not fail within 5s")
			}
			if err == nil {
				t.Fatal("run() error = nil, want a startup failure")
			}
			for _, want := range tt.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("run() error = %q, want it to contain %q", err, want)
				}
			}
			events, _ := s.rec.snapshot()
			if !slices.Equal(events, tt.wantEvents) {
				t.Errorf("events = %q, want %q", events, tt.wantEvents)
			}
			if _, err := os.Lstat(s.cfg.Socket); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("socket stat error = %v, want not exist", err)
			}
		})
	}
}

// foreignGID returns a group the process is not a member of.
func foreignGID(t *testing.T) int {
	t.Helper()
	groups, err := os.Getgroups()
	if err != nil {
		t.Fatalf("reading groups: %v", err)
	}
	for gid := 4242; ; gid++ {
		if gid != os.Getegid() && !slices.Contains(groups, gid) {
			return gid
		}
	}
}

// supplementaryGID returns a group of the process other than its effective group, or skips.
func supplementaryGID(t *testing.T) int {
	t.Helper()
	groups, err := os.Getgroups()
	if err != nil {
		t.Fatalf("reading groups: %v", err)
	}
	for _, gid := range groups {
		if gid != os.Getegid() {
			return gid
		}
	}
	t.Skip("the process has no supplementary group to give the socket")
	return 0
}

func TestRunReplacesStaleSocket(t *testing.T) {
	s := newStartup(t, nil)
	lis, err := net.Listen("unix", s.cfg.Socket)
	if err != nil {
		t.Fatalf("creating stale socket: %v", err)
	}
	lis.(*net.UnixListener).SetUnlinkOnClose(false)
	if err := lis.Close(); err != nil {
		t.Fatalf("closing stale socket: %v", err)
	}
	if _, err := os.Lstat(s.cfg.Socket); err != nil {
		t.Fatalf("stale socket missing before run: %v", err)
	}

	// serving fails the test unless a health check passes over the path, which a stale socket
	// file cannot answer.
	s.serving(t)
}

func TestRunSocketPermissions(t *testing.T) {
	// Root can give the socket any group; another user only one of its supplementary groups. The
	// group set differs from the effective group, so a missing chown fails the row.
	tests := []struct {
		name string
		gid  func(t *testing.T) int
	}{
		{name: "group unset keeps the process group", gid: func(*testing.T) int { return noSocketGID }},
		{name: "group set", gid: func(t *testing.T) int {
			if os.Geteuid() == 0 {
				return foreignGID(t)
			}
			return supplementaryGID(t)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gid := tt.gid(t)
			wantGID := gid
			if gid == noSocketGID {
				wantGID = os.Getegid()
			}
			s := newStartup(t, nil)
			s.cfg.SocketGID = gid
			s.serving(t)

			fi, err := os.Lstat(s.cfg.Socket)
			if err != nil {
				t.Fatalf("stat socket: %v", err)
			}
			if fi.Mode().Type() != fs.ModeSocket {
				t.Errorf("socket type = %v, want a socket", fi.Mode().Type())
			}
			if perm := fi.Mode().Perm(); perm != 0o660 {
				t.Errorf("socket mode = %04o, want 0660", perm)
			}
			if got := int(fi.Sys().(*syscall.Stat_t).Gid); got != wantGID {
				t.Errorf("socket group = %d, want %d", got, wantGID)
			}
		})
	}
}

func TestRunServesManagementAndStopsOnCancel(t *testing.T) {
	s := newStartup(t, nil)
	cancel := s.serving(t)

	conn, err := grpc.NewClient("unix:"+s.cfg.Socket, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dialing socket: %v", err)
	}
	defer func() { _ = conn.Close() }()
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	_, err = awgv1.NewManagementServiceClient(conn).ListPeers(ctx, &awgv1.ListPeersRequest{InterfaceName: "awg9"})
	if status.Code(err) != codes.NotFound {
		t.Errorf("ListPeers(awg9) code = %v, want NotFound", status.Code(err))
	}

	if err := cancel(); err != nil {
		t.Fatalf("run() after cancel = %v, want nil", err)
	}
	if _, err := os.Lstat(s.cfg.Socket); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("socket stat error after stop = %v, want not exist", err)
	}
}

func TestRunRecoversHandlerPanic(t *testing.T) {
	s := newStartup(t, nil)
	s.runner.panicOn = "awg show awg0 dump"
	cancel := s.serving(t)

	conn, err := grpc.NewClient("unix:"+s.cfg.Socket, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dialing socket: %v", err)
	}
	defer func() { _ = conn.Close() }()
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	_, err = awgv1.NewManagementServiceClient(conn).ListPeers(ctx, &awgv1.ListPeersRequest{InterfaceName: "awg0"})
	if status.Code(err) != codes.Internal {
		t.Errorf("ListPeers(awg0) with a panicking handler: code = %v, want Internal", status.Code(err))
	}

	if code := healthcheck(context.Background(), s.cfg.Socket, t.Output()); code != 0 {
		t.Errorf("healthcheck after the panic = %d, want 0", code)
	}
	if err := cancel(); err != nil {
		t.Fatalf("run() after cancel = %v, want nil", err)
	}
}
