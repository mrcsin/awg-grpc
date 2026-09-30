package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"

	awgv1 "github.com/mrcsin/awg-grpc/gen/awg/v1"
	"github.com/mrcsin/awg-grpc/internal/awg"
	"github.com/mrcsin/awg-grpc/internal/awg/awgtest"
)

// Test interfaces: awg0 is configured and present, awg1 is configured and absent.
const (
	presentIface = "awg0"
	absentIface  = "awg1"
)

var dumpArgs = []string{"show", presentIface, "dump"}

func key(b byte) awg.Key {
	var k awg.Key
	for i := range k {
		k[i] = b
	}
	return k
}

// serverKey is the public key of awg0 in every generated dump.
var serverKey = key(0xEE)

// deviceLine is an awg show dump device line with 29 fields and default obfuscation values.
func deviceLine(pub awg.Key) string {
	fields := []string{key(0xDD).String(), pub.String(), "51820", "0", "0", "0", "0", "0", "0", "0",
		"1", "2", "3", "4", "(null)", "(null)", "(null)", "(null)", "(null)", "(none)",
		"0", "0", "0", "0", "0", "0", "off", "off", "off"}
	return strings.Join(fields, "\t")
}

// peerLine is an awg show dump peer line; sharedKey 0 means no preshared key.
func peerLine(pub, sharedKey byte, cidr string) string {
	pskText := "(none)"
	if sharedKey != 0 {
		pskText = key(sharedKey).String()
	}
	return strings.Join([]string{key(pub).String(), pskText, "(none)", cidr, "0", "0", "0", "off"}, "\t")
}

func dump(peerLines ...string) []byte {
	return []byte(strings.Join(append([]string{deviceLine(serverKey)}, peerLines...), "\n") + "\n")
}

func reqPeer(pub, sharedKey byte, cidr string) *awgv1.Peer {
	pk := key(pub)
	sk := key(sharedKey)
	return &awgv1.Peer{PublicKey: pk[:], PresharedKey: sk[:], AllowedIp: cidr}
}

func keyBytes(bs ...byte) [][]byte {
	out := make([][]byte, 0, len(bs))
	for _, b := range bs {
		k := key(b)
		out = append(out, k[:])
	}
	return out
}

func lookupOf(addrs map[string]string) Lookup {
	return func(name string) ([]netip.Prefix, bool, error) {
		addr, ok := addrs[name]
		if !ok {
			return nil, false, nil
		}
		return []netip.Prefix{netip.MustParsePrefix(addr)}, true, nil
	}
}

var awg0Lookup = lookupOf(map[string]string{presentIface: "10.8.1.1/24"})

// syncBuffer is a log sink safe for the server goroutines and the test goroutine.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func startServer(t *testing.T, runner awg.Runner, logOut io.Writer) awgv1.ManagementServiceClient {
	t.Helper()
	return serveManagement(t,
		NewManagement(runner, awg0Lookup, []string{presentIface, absentIface}, Versions{}), logOut)
}

func serveManagement(t *testing.T, m *Management, logOut io.Writer) awgv1.ManagementServiceClient {
	t.Helper()
	conn := serveBufconn(t, logOut, func(srv *grpc.Server) { awgv1.RegisterManagementServiceServer(srv, m) })
	return awgv1.NewManagementServiceClient(conn)
}

func serveBufconn(t *testing.T, logOut io.Writer, register func(*grpc.Server)) *grpc.ClientConn {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(logOut, nil))
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(RecoverUnary(logger)))
	register(srv)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dialing bufconn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func assertCode(t *testing.T, err error, want codes.Code) *status.Status {
	t.Helper()
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("error %v is not a gRPC status", err)
	}
	if st.Code() != want {
		t.Fatalf("code = %s (%q), want %s", st.Code(), st.Message(), want)
	}
	return st
}

func assertNoSet(t *testing.T, calls []awg.Command) {
	t.Helper()
	for _, c := range calls {
		if len(c.Args) > 0 && c.Args[0] == "set" {
			t.Fatalf("unexpected awg set call: %q", c.Args)
		}
	}
}

func assertNoKeyText(t *testing.T, text string, keys ...awg.Key) {
	t.Helper()
	for _, k := range keys {
		for _, form := range awgtest.KeyForms(k[:]) {
			if strings.Contains(text, form) {
				t.Fatalf("text holds key %s: %q", k, text)
			}
		}
	}
}

func TestApplyPeersInterfaceErrors(t *testing.T) {
	tests := []struct {
		name     string
		iface    string
		wantCode codes.Code
		wantText string
	}{
		{
			name:     "unknown interface with a newline is NOT_FOUND and quoted",
			iface:    "awg0\n",
			wantCode: codes.NotFound,
			wantText: `"awg0\n"`,
		},
		{
			name:     "configured but absent interface is FAILED_PRECONDITION",
			iface:    absentIface,
			wantCode: codes.FailedPrecondition,
			wantText: absentIface,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := awgtest.NewRunner()
			client := startServer(t, runner, io.Discard)
			_, err := client.ApplyPeers(context.Background(), &awgv1.ApplyPeersRequest{
				InterfaceName: tt.iface,
				Peers:         []*awgv1.Peer{reqPeer(0x01, 0x11, "10.8.1.2/32")},
			})
			st := assertCode(t, err, tt.wantCode)
			if !strings.Contains(st.Message(), tt.wantText) {
				t.Errorf("message %q does not hold %s", st.Message(), tt.wantText)
			}
			if strings.ContainsAny(st.Message(), "\n\r") {
				t.Errorf("message %q holds a line break", st.Message())
			}
			if calls := runner.Calls(); len(calls) != 0 {
				t.Errorf("runner calls = %v, want none", calls)
			}
		})
	}
}

func TestApplyPeersValidation(t *testing.T) {
	// wantText is text only the expected rule writes.
	tests := []struct {
		name     string
		peers    []*awgv1.Peer
		wantText string
	}{
		{name: "empty list without allow_empty", wantText: "peer list is empty"},
		{name: "overlapping addresses", peers: []*awgv1.Peer{
			reqPeer(0x01, 0x11, "10.8.1.2/32"), reqPeer(0x02, 0x12, "10.8.1.2/32")},
			wantText: "overlaps 10.8.1.2/32 of peer 0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := awgtest.NewRunner()
			runner.Respond("awg", dumpArgs, dump(), nil)
			client := startServer(t, runner, io.Discard)
			_, err := client.ApplyPeers(context.Background(), &awgv1.ApplyPeersRequest{
				InterfaceName: presentIface,
				Peers:         tt.peers,
			})
			st := assertCode(t, err, codes.InvalidArgument)
			if !strings.Contains(st.Message(), tt.wantText) {
				t.Errorf("message %q does not hold %q", st.Message(), tt.wantText)
			}
			assertNoKeyText(t, st.Message(), key(0x11), key(0x12))
			assertNoSet(t, runner.Calls())
		})
	}
}

// applyScenario is a kernel before and after an apply of desiredPeers. The after dump lacks
// peer 0x05, so a response built from it differs from the kernel-to-desired diff.
var (
	beforeDump = dump(
		peerLine(0x01, 0x11, "10.8.1.2/32"),
		peerLine(0x02, 0, "10.8.1.3/32"),
		peerLine(0x03, 0x13, "10.8.1.4/32"),
	)
	afterDump = dump(
		peerLine(0x01, 0x11, "10.8.1.2/32"),
		peerLine(0x02, 0x12, "10.8.1.3/32"),
		peerLine(0x04, 0x14, "10.8.1.5/32"),
	)
	desiredPeers = []*awgv1.Peer{
		reqPeer(0x01, 0x11, "10.8.1.2/32"),
		reqPeer(0x02, 0x12, "10.8.1.3/32"),
		reqPeer(0x04, 0x14, "10.8.1.5/32"),
		reqPeer(0x05, 0x15, "10.8.1.6/32"),
	}
	setArgs = []string{
		"set", presentIface,
		"peer", key(0x03).String(), "remove",
		"peer", key(0x04).String(), "allowed-ips", "10.8.1.5/32", "preshared-key", "/dev/fd/3",
		"peer", key(0x05).String(), "allowed-ips", "10.8.1.6/32", "preshared-key", "/dev/fd/4",
		"peer", key(0x02).String(), "allowed-ips", "10.8.1.3/32", "preshared-key", "/dev/fd/5",
	}
)

func assertChanges(t *testing.T, got *awgv1.ApplyPeersResponse, added, removed, updated [][]byte) {
	t.Helper()
	for _, c := range []struct {
		name      string
		got, want [][]byte
	}{
		{"added", got.GetAdded(), added},
		{"removed", got.GetRemoved(), removed},
		{"updated", got.GetUpdated(), updated},
	} {
		if !slices.EqualFunc(c.got, c.want, bytes.Equal) {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func commandArgs(calls []awg.Command) [][]string {
	out := make([][]string, 0, len(calls))
	for _, c := range calls {
		out = append(out, append([]string{c.Name}, c.Args...))
	}
	return out
}

func TestApplyPeersDryRun(t *testing.T) {
	runner := awgtest.NewRunner()
	runner.Respond("awg", dumpArgs, beforeDump, nil)
	client := startServer(t, runner, io.Discard)

	resp, err := client.ApplyPeers(context.Background(), &awgv1.ApplyPeersRequest{
		InterfaceName: presentIface,
		Peers:         desiredPeers,
		DryRun:        true,
	})
	if err != nil {
		t.Fatalf("ApplyPeers: %v", err)
	}
	assertChanges(t, resp, keyBytes(0x04, 0x05), keyBytes(0x03), keyBytes(0x02))
	calls := runner.Calls()
	if len(calls) != 1 {
		t.Errorf("calls = %q, want one dump", commandArgs(calls))
	}
	assertNoSet(t, calls)
}

func TestApplyPeersApplies(t *testing.T) {
	runner := awgtest.NewRunner()
	runner.Respond("awg", dumpArgs, beforeDump, nil)
	runner.Respond("awg", dumpArgs, afterDump, nil)
	runner.Respond("awg", setArgs, nil, nil)
	client := startServer(t, runner, io.Discard)

	resp, err := client.ApplyPeers(context.Background(), &awgv1.ApplyPeersRequest{
		InterfaceName: presentIface,
		Peers:         desiredPeers,
	})
	if err != nil {
		t.Fatalf("ApplyPeers: %v", err)
	}
	// The before-after diff: 0x05 never reached the kernel, so it is not reported as added.
	assertChanges(t, resp, keyBytes(0x04), keyBytes(0x03), keyBytes(0x02))

	calls := runner.Calls()
	want := [][]string{
		append([]string{"awg"}, dumpArgs...),
		append([]string{"awg"}, setArgs...),
		append([]string{"awg"}, dumpArgs...),
	}
	if got := commandArgs(calls); !slices.EqualFunc(got, want, slices.Equal) {
		t.Fatalf("calls = %q, want %q", got, want)
	}
	if n := len(calls[1].ExtraFiles); n != 3 {
		t.Errorf("awg set extra files = %d, want 3", n)
	}
	assertNoKeyText(t, strings.Join(calls[1].Args, " "), key(0x12), key(0x14), key(0x15))
}

func TestApplyPeersNoChangesSkipsSet(t *testing.T) {
	runner := awgtest.NewRunner()
	runner.Respond("awg", dumpArgs, afterDump, nil)
	client := startServer(t, runner, io.Discard)

	resp, err := client.ApplyPeers(context.Background(), &awgv1.ApplyPeersRequest{
		InterfaceName: presentIface,
		Peers:         desiredPeers[:3],
	})
	if err != nil {
		t.Fatalf("ApplyPeers: %v", err)
	}
	assertChanges(t, resp, nil, nil, nil)
	if calls := runner.Calls(); len(calls) != 1 {
		t.Errorf("calls = %q, want one dump", commandArgs(calls))
	}
}

func TestApplyPeersInternalErrors(t *testing.T) {
	sharedKey := key(0x14)
	exitErr := fmt.Errorf("running awg: %w", &awg.ExitError{
		Code:   1,
		Stderr: "Key is not the correct length or format: `***'\n",
	})
	tests := []struct {
		name     string
		respond  func(r *awgtest.Runner)
		wantText string
	}{
		{
			name: "awg set fails",
			respond: func(r *awgtest.Runner) {
				r.Respond("awg", dumpArgs, beforeDump, nil)
				r.Respond("awg", setArgs, nil, exitErr)
			},
			wantText: "exit status 1",
		},
		{
			name: "dump fails",
			respond: func(r *awgtest.Runner) {
				r.Respond("awg", dumpArgs, nil, exitErr)
			},
			wantText: "exit status 1",
		},
		{
			name: "dump does not parse",
			respond: func(r *awgtest.Runner) {
				r.Respond("awg", dumpArgs, []byte("garbage\n"), nil)
			},
			wantText: "fields",
		},
		{
			name: "dump after awg set fails",
			respond: func(r *awgtest.Runner) {
				r.Respond("awg", dumpArgs, beforeDump, nil)
				r.Respond("awg", dumpArgs, nil, exitErr)
				r.Respond("awg", setArgs, nil, nil)
			},
			wantText: "exit status 1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := awgtest.NewRunner()
			tt.respond(runner)
			client := startServer(t, runner, io.Discard)
			_, err := client.ApplyPeers(context.Background(), &awgv1.ApplyPeersRequest{
				InterfaceName: presentIface,
				Peers:         desiredPeers,
			})
			st := assertCode(t, err, codes.Internal)
			if !strings.Contains(st.Message(), tt.wantText) {
				t.Errorf("message %q does not hold %q", st.Message(), tt.wantText)
			}
			assertNoKeyText(t, st.Message(), key(0x11), key(0x12), sharedKey, key(0x15))
		})
	}
}

// gateRunner holds every awg set on awg0 until release is closed and reports every dump of awg0
// on awg0Dumps.
type gateRunner struct {
	*awgtest.Runner
	awg0Dumps chan struct{}
	entered   chan struct{}
	release   chan struct{}
}

func newGateRunner(fake *awgtest.Runner) *gateRunner {
	return &gateRunner{
		Runner:    fake,
		awg0Dumps: make(chan struct{}, 8),
		entered:   make(chan struct{}, 2),
		release:   make(chan struct{}),
	}
}

func (g *gateRunner) Run(ctx context.Context, cmd awg.Command) ([]byte, error) {
	switch {
	case slices.Equal(cmd.Args, dumpArgs):
		g.awg0Dumps <- struct{}{}
	case len(cmd.Args) > 1 && cmd.Args[0] == "set" && cmd.Args[1] == presentIface:
		g.entered <- struct{}{}
		<-g.release
	}
	return g.Runner.Run(ctx, cmd)
}

// awg0Calls returns the calls on awg0, name first.
func awg0Calls(calls []awg.Command) [][]string {
	var out [][]string
	for _, c := range commandArgs(calls) {
		if len(c) > 2 && c[2] == presentIface {
			out = append(out, c)
		}
	}
	return out
}

// secondSetArgs is the command, name first, that adds the peer afterDump lacks.
var secondSetArgs = []string{"awg", "set", presentIface,
	"peer", key(0x05).String(), "allowed-ips", "10.8.1.6/32", "preshared-key", "/dev/fd/3"}

// otherIface is a second present interface, with the address 10.8.2.1/24.
const otherIface = "awg2"

func TestApplyPeersSerializesPerInterface(t *testing.T) {
	otherDumpArgs := []string{"show", otherIface, "dump"}
	otherSetArgs := []string{"set", otherIface,
		"peer", key(0x21).String(), "allowed-ips", "10.8.2.2/32", "preshared-key", "/dev/fd/3"}
	fake := awgtest.NewRunner()
	fake.Respond("awg", dumpArgs, beforeDump, nil)
	fake.Respond("awg", dumpArgs, afterDump, nil)
	fake.Respond("awg", setArgs, nil, nil)
	fake.Respond("awg", secondSetArgs[1:], nil, nil)
	fake.Respond("awg", otherDumpArgs, dump(), nil)
	fake.Respond("awg", otherDumpArgs, dump(peerLine(0x21, 0x31, "10.8.2.2/32")), nil)
	fake.Respond("awg", otherSetArgs, nil, nil)
	gate := newGateRunner(fake)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(gate.release) }) }
	t.Cleanup(release)
	client := serveManagement(t,
		NewManagement(gate, lookupOf(map[string]string{presentIface: "10.8.1.1/24", otherIface: "10.8.2.1/24"}),
			[]string{presentIface, otherIface}, Versions{}), io.Discard)

	req := &awgv1.ApplyPeersRequest{InterfaceName: presentIface, Peers: desiredPeers}
	var wg sync.WaitGroup
	responses := make([]*awgv1.ApplyPeersResponse, 2)
	errs := make([]error, 2)
	apply := func(i int) {
		defer wg.Done()
		responses[i], errs[i] = client.ApplyPeers(context.Background(), req)
	}

	wg.Add(1)
	go apply(0)
	<-gate.awg0Dumps
	<-gate.entered
	wg.Add(1)
	go apply(1)

	// The lock is per interface: awg2 applies while the first apply on awg0 holds its lock.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	other, err := client.ApplyPeers(ctx, &awgv1.ApplyPeersRequest{
		InterfaceName: otherIface,
		Peers:         []*awgv1.Peer{reqPeer(0x21, 0x31, "10.8.2.2/32")},
	})
	if err != nil {
		t.Fatalf("apply on %s while %s is locked: %v", otherIface, presentIface, err)
	}
	assertChanges(t, other, keyBytes(0x21), nil, nil)

	select {
	case <-gate.awg0Dumps:
		t.Error("the second apply on awg0 read the dump while the first one held the lock")
	case <-time.After(100 * time.Millisecond):
	}
	release()
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("apply %d: %v", i, err)
		}
	}
	assertChanges(t, responses[0], keyBytes(0x04), keyBytes(0x03), keyBytes(0x02))
	// The second apply starts from the kernel the first one left: only 0x05 is still missing.
	calls := awg0Calls(fake.Calls())
	if len(calls) < 5 || !slices.Equal(calls[4], secondSetArgs) {
		t.Errorf("awg0 calls = %q, want dump, set, dump, dump, then %q", calls, secondSetArgs)
	}
}

// handlerDone sends the error of every ApplyPeers handler on done when the handler returns.
type handlerDone struct {
	*Management
	done chan error
}

func (h handlerDone) ApplyPeers(ctx context.Context, req *awgv1.ApplyPeersRequest) (*awgv1.ApplyPeersResponse, error) {
	resp, err := h.Management.ApplyPeers(ctx, req)
	h.done <- err
	return resp, err
}

func TestApplyPeersWaitingForTheLockEndsWithTheContext(t *testing.T) {
	fake := awgtest.NewRunner()
	fake.Respond("awg", dumpArgs, beforeDump, nil)
	fake.Respond("awg", dumpArgs, afterDump, nil)
	fake.Respond("awg", setArgs, nil, nil)
	gate := newGateRunner(fake)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(gate.release) }) }
	t.Cleanup(release)
	handlers := handlerDone{
		Management: NewManagement(gate, awg0Lookup, []string{presentIface}, Versions{}),
		done:       make(chan error, 2),
	}
	client := awgv1.NewManagementServiceClient(serveBufconn(t, io.Discard, func(srv *grpc.Server) {
		awgv1.RegisterManagementServiceServer(srv, handlers)
	}))

	req := &awgv1.ApplyPeersRequest{InterfaceName: presentIface, Peers: desiredPeers}
	first := make(chan error, 1)
	go func() {
		_, err := client.ApplyPeers(context.Background(), req)
		first <- err
	}()
	<-gate.awg0Dumps
	<-gate.entered

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := client.ApplyPeers(ctx, req)
	assertCode(t, err, codes.DeadlineExceeded)
	// The server context ends by its own deadline, a moment after the client's, or by the
	// client's cancel. The lock is released only after the waiting handler has returned.
	select {
	case err := <-handlers.done:
		if code := status.Code(err); code != codes.DeadlineExceeded && code != codes.Canceled {
			t.Fatalf("waiting handler code = %v (%v), want a deadline or a cancel", code, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the waiting handler did not return after its context ended")
	}
	release()
	if err := <-first; err != nil {
		t.Fatalf("first apply: %v", err)
	}
	<-handlers.done

	// Every handler has returned, so the calls on awg0 are final: the waiter made none.
	want := [][]string{
		append([]string{"awg"}, dumpArgs...),
		append([]string{"awg"}, setArgs...),
		append([]string{"awg"}, dumpArgs...),
	}
	if calls := awg0Calls(fake.Calls()); !slices.EqualFunc(calls, want, slices.Equal) {
		t.Errorf("awg0 calls = %q, want %q", calls, want)
	}
}

// blockingRunner blocks the command block until the call context ends and sends the context error
// it saw on seen. Every other command goes to the fake.
type blockingRunner struct {
	*awgtest.Runner
	block []string
	seen  chan error
}

func (b *blockingRunner) Run(ctx context.Context, cmd awg.Command) ([]byte, error) {
	if slices.Equal(append([]string{cmd.Name}, cmd.Args...), b.block) {
		<-ctx.Done()
		b.seen <- ctx.Err()
		return nil, fmt.Errorf("running %s: %w", cmd.Name, ctx.Err())
	}
	return b.Runner.Run(ctx, cmd)
}

func TestClientDeadlineEndsTheRunnerContext(t *testing.T) {
	tests := []struct {
		name  string
		block []string
		call  func(context.Context, awgv1.ManagementServiceClient) error
	}{
		{
			name:  "ApplyPeers in awg set",
			block: append([]string{"awg"}, setArgs...),
			call: func(ctx context.Context, c awgv1.ManagementServiceClient) error {
				_, err := c.ApplyPeers(ctx, &awgv1.ApplyPeersRequest{InterfaceName: presentIface, Peers: desiredPeers})
				return err
			},
		},
		{
			name:  "ListPeers in awg show dump",
			block: append([]string{"awg"}, dumpArgs...),
			call: func(ctx context.Context, c awgv1.ManagementServiceClient) error {
				_, err := c.ListPeers(ctx, &awgv1.ListPeersRequest{InterfaceName: presentIface})
				return err
			},
		},
		{
			name:  "GetStatus in awg showconf",
			block: append([]string{"awg"}, showconfArgs...),
			call: func(ctx context.Context, c awgv1.ManagementServiceClient) error {
				_, err := c.GetStatus(ctx, &awgv1.GetStatusRequest{})
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := awgtest.NewRunner()
			fake.Respond("awg", dumpArgs, beforeDump, nil)
			runner := &blockingRunner{Runner: fake, block: tt.block, seen: make(chan error, 1)}
			m := NewManagement(runner, awg0Lookup, []string{presentIface},
				Versions{ModuleVersionFile: writeModuleVersion(t, "3.1.20260812\n")})
			client := serveManagement(t, m, io.Discard)

			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			assertCode(t, tt.call(ctx, client), codes.DeadlineExceeded)
			select {
			case err := <-runner.seen:
				// The server context ends by its own deadline or by the client's cancel, whichever
				// arrives first; a handler that dropped ctx would never get here.
				if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
					t.Errorf("runner context error = %v, want a deadline or a cancel", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the runner context never ended after the client deadline")
			}
		})
	}
}

func readFixture(t *testing.T, parts ...string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{"..", "..", "testdata"}, parts...)...))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	return b
}

func mustDecode(t *testing.T, text string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		t.Fatalf("decoding fixture key: %v", err)
	}
	return b
}

func TestListPeers(t *testing.T) {
	fixture := readFixture(t, "dump", "full.txt")
	runner := awgtest.NewRunner()
	runner.Respond("awg", dumpArgs, fixture, nil)
	client := startServer(t, runner, io.Discard)

	resp, err := client.ListPeers(context.Background(), &awgv1.ListPeersRequest{InterfaceName: presentIface})
	if err != nil {
		t.Fatalf("ListPeers: %v", err)
	}
	want := []struct {
		publicKey string
		allowed   []string
		endpoint  string
		handshake int64 // 0 means unset
		rx, tx    uint64
	}{
		{"ij+isP0PDb6e0Qlejysg6hgcy+izwRa/mw/RMnbUGGk=", []string{"10.67.0.2/32"}, "172.20.0.3:51824", 1790709091, 567, 874},
		{"lLks0INSyThw+sM+JDZYt0a4kCk7Tj9EckexdWncqxg=", []string{"10.67.0.8/30", "10.67.0.12/30"}, "", 0, 0, 0},
		{"lj1uEIQdYBX+h1aAi5yfinsbMHv5tmQGqm/iPLthKj4=", []string{"10.67.0.20/32"}, "172.20.0.250:51820", 0, 0, 1247},
		{"GSp+2CtxxUfOMjfp2hpw620fKbYgjeixIxXsYpJGHwU=", []string{"10.67.0.21/32"}, "[2001:db8::1]:51820", 0, 0, 0},
		{"69czXD28b5Q4ZYoy4db3vDKtpmmhZ76OkFeKhqzX7F0=", nil, "", 0, 0, 0},
	}
	peers := resp.GetPeers()
	if len(peers) != len(want) {
		t.Fatalf("peers = %d, want %d", len(peers), len(want))
	}
	for i, w := range want {
		p := peers[i]
		if !bytes.Equal(p.GetPublicKey(), mustDecode(t, w.publicKey)) {
			t.Errorf("peer %d public key = %s, want %s", i,
				base64.StdEncoding.EncodeToString(p.GetPublicKey()), w.publicKey)
		}
		if !slices.Equal(p.GetAllowedIps(), w.allowed) {
			t.Errorf("peer %d allowed IPs = %q, want %q", i, p.GetAllowedIps(), w.allowed)
		}
		if p.GetEndpoint() != w.endpoint {
			t.Errorf("peer %d endpoint = %q, want %q", i, p.GetEndpoint(), w.endpoint)
		}
		switch {
		case w.handshake == 0 && p.GetLastHandshake() != nil:
			t.Errorf("peer %d last handshake = %v, want unset", i, p.GetLastHandshake())
		case w.handshake != 0 && p.GetLastHandshake().GetSeconds() != w.handshake:
			t.Errorf("peer %d last handshake = %v, want %d", i, p.GetLastHandshake(), w.handshake)
		}
		if p.GetRxBytes() != w.rx || p.GetTxBytes() != w.tx {
			t.Errorf("peer %d rx/tx = %d/%d, want %d/%d", i, p.GetRxBytes(), p.GetTxBytes(), w.rx, w.tx)
		}
	}

	wire, err := proto.Marshal(resp)
	if err != nil {
		t.Fatalf("marshalling response: %v", err)
	}
	for _, pskText := range []string{
		"wY6yc+47yV+7NrIKbYTEnsMRTfnkTXkf8rP1u41TbAw=",
		"pjLJpdDVySwpEs2bu1kTLlXWHI8qmmaEdH/0Ti0S0ME=",
	} {
		raw := mustDecode(t, pskText)
		if bytes.Contains(wire, raw) || bytes.Contains(wire, []byte(pskText)) {
			t.Errorf("response carries preshared key %s", pskText)
		}
	}
}

func TestListPeersErrors(t *testing.T) {
	tests := []struct {
		name     string
		iface    string
		respond  []byte
		wantCode codes.Code
	}{
		{name: "unknown interface", iface: "awg9", wantCode: codes.NotFound},
		{name: "absent interface", iface: absentIface, wantCode: codes.FailedPrecondition},
		{name: "dump does not parse", iface: presentIface, respond: []byte("x\n"), wantCode: codes.Internal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := awgtest.NewRunner()
			runner.Respond("awg", dumpArgs, tt.respond, nil)
			client := startServer(t, runner, io.Discard)
			_, err := client.ListPeers(context.Background(), &awgv1.ListPeersRequest{InterfaceName: tt.iface})
			assertCode(t, err, tt.wantCode)
		})
	}
}

func TestStatusFromContextErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want codes.Code
	}{
		{"deadline", fmt.Errorf("running awg: %w", context.DeadlineExceeded), codes.DeadlineExceeded},
		{"cancel", fmt.Errorf("running awg: %w", context.Canceled), codes.Canceled},
		{"other", errors.New("boom"), codes.Internal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := status.Code(toStatus(tt.err)); got != tt.want {
				t.Errorf("code = %s, want %s", got, tt.want)
			}
		})
	}
}

var showconfArgs = []string{"showconf", presentIface}

// writeModuleVersion writes a stand-in for /sys/module/amneziawg/version and returns its path.
func writeModuleVersion(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "version")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing module version: %v", err)
	}
	return path
}

// statusRunner answers the dump and showconf of awg0 from the full fixtures.
func statusRunner(t *testing.T) *awgtest.Runner {
	t.Helper()
	runner := awgtest.NewRunner()
	runner.Respond("awg", dumpArgs, readFixture(t, "dump", "full.txt"), nil)
	runner.Respond("awg", showconfArgs, readFixture(t, "showconf", "full.conf"), nil)
	return runner
}

func TestGetStatus(t *testing.T) {
	runner := statusRunner(t)
	versions := Versions{
		Wrapper:           "v1.2.3",
		Tools:             "3.1.20260812",
		ModuleVersionFile: writeModuleVersion(t, "3.1.20260812\n"),
	}
	client := serveManagement(t,
		NewManagement(runner, awg0Lookup, []string{presentIface, absentIface}, versions), io.Discard)

	resp, err := client.GetStatus(context.Background(), &awgv1.GetStatusRequest{})
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if got, want := []string{resp.GetWrapperVersion(), resp.GetToolsVersion(), resp.GetModuleVersion()},
		[]string{"v1.2.3", "3.1.20260812", "3.1.20260812"}; !slices.Equal(got, want) {
		t.Errorf("wrapper, tools, module versions = %q, want %q", got, want)
	}

	var params []*awgv1.ConfigParam
	for _, kv := range [][2]string{
		{"Jc", "4"}, {"Jmin", "40"}, {"Jmax", "70"},
		{"S1", "15"}, {"S2", "18"}, {"S3", "20"}, {"S4", "23"},
		{"H1", "100000-199999"}, {"H2", "200000-299999"}, {"H3", "300000-399999"}, {"H4", "400000-499999"},
		{"I1", "<b 0xc6000000010801> <r 16> <c> <t>"}, {"I2", "<r 32>"}, {"I3", "<rc 8>"},
		{"I4", "<rd 4>"}, {"I5", "<b 0xdeadbeef>"},
		{"HeaderProtectionKey", "kGvokoudkDy4Oc9m8UeLE8OBSKrhbNhxOLydN5Np6Vw="},
		{"ContentPaddingAddition", "4-16"}, {"RekeyAfterTime", "110"}, {"RekeyTimeout", "4"},
		{"RejectAfterTime", "170"}, {"KeepaliveTimeout", "9"}, {"MaxHandshakeAttempts", "15"},
		{"RandomTrailers", "on"}, {"DisableCookies", "on"},
	} {
		params = append(params, &awgv1.ConfigParam{Key: kv[0], Value: kv[1]})
	}
	want := []*awgv1.InterfaceStatus{
		{
			Name:         presentIface,
			Present:      true,
			PublicKey:    mustDecode(t, "+tJYTAbxDVzZ8a/WIgKkCoNBeXrX451uuqlW7V5NnlY="),
			ListenPort:   51823,
			PeerCount:    5,
			Addresses:    []string{"10.8.1.1/24"},
			ClientParams: params,
		},
		{Name: absentIface},
	}
	got := resp.GetInterfaces()
	if len(got) != len(want) {
		t.Fatalf("interfaces = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if !proto.Equal(got[i], want[i]) {
			t.Errorf("interface %d = %v\nwant %v", i, got[i], want[i])
		}
	}

	wantCalls := [][]string{append([]string{"awg"}, dumpArgs...), append([]string{"awg"}, showconfArgs...)}
	if calls := commandArgs(runner.Calls()); !slices.EqualFunc(calls, wantCalls, slices.Equal) {
		t.Errorf("calls = %q, want %q", calls, wantCalls)
	}

	wire, err := proto.Marshal(resp)
	if err != nil {
		t.Fatalf("marshalling response: %v", err)
	}
	privateKey := "cHFl52dULmNk8OoKCShfd6WdxqlD+HJRkxPezBbmk1A="
	if bytes.Contains(wire, mustDecode(t, privateKey)) || bytes.Contains(wire, []byte(privateKey)) {
		t.Error("response carries the interface private key")
	}
}

func TestGetStatusModuleVersion(t *testing.T) {
	tests := []struct {
		name     string
		path     func(t *testing.T) string
		want     string
		wantCode codes.Code
	}{
		{
			name: "trimmed file content",
			path: func(t *testing.T) string { return writeModuleVersion(t, " 3.1.20260812 \n") },
			want: "3.1.20260812",
		},
		{
			name: "module not loaded reports an empty version",
			path: func(t *testing.T) string { return filepath.Join(t.TempDir(), "absent") },
			want: "",
		},
		{
			name:     "unreadable file is INTERNAL",
			path:     func(t *testing.T) string { return t.TempDir() },
			wantCode: codes.Internal,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewManagement(statusRunner(t), awg0Lookup, []string{presentIface},
				Versions{ModuleVersionFile: tt.path(t)})
			client := serveManagement(t, m, io.Discard)
			resp, err := client.GetStatus(context.Background(), &awgv1.GetStatusRequest{})
			if tt.wantCode != codes.OK {
				assertCode(t, err, tt.wantCode)
				return
			}
			if err != nil {
				t.Fatalf("GetStatus: %v", err)
			}
			if got := resp.GetModuleVersion(); got != tt.want {
				t.Errorf("module version = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetStatusErrors(t *testing.T) {
	exitErr := &awg.ExitError{Code: 1, Stderr: "Unable to access interface: No such device\n"}
	tests := []struct {
		name     string
		respond  func(r *awgtest.Runner)
		wantText string
	}{
		{
			name: "dump fails",
			respond: func(r *awgtest.Runner) {
				r.Respond("awg", dumpArgs, nil, exitErr)
			},
			wantText: "exit status 1",
		},
		{
			name: "showconf fails",
			respond: func(r *awgtest.Runner) {
				r.Respond("awg", dumpArgs, dump(), nil)
				r.Respond("awg", showconfArgs, nil, exitErr)
			},
			wantText: "exit status 1",
		},
		{
			name: "showconf does not parse",
			respond: func(r *awgtest.Runner) {
				r.Respond("awg", dumpArgs, dump(), nil)
				r.Respond("awg", showconfArgs, []byte("garbage\n"), nil)
			},
			wantText: "[Interface]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := awgtest.NewRunner()
			tt.respond(runner)
			m := NewManagement(runner, awg0Lookup, []string{presentIface},
				Versions{ModuleVersionFile: writeModuleVersion(t, "3.1.20260812\n")})
			client := serveManagement(t, m, io.Discard)
			_, err := client.GetStatus(context.Background(), &awgv1.GetStatusRequest{})
			st := assertCode(t, err, codes.Internal)
			if !strings.Contains(st.Message(), tt.wantText) {
				t.Errorf("message %q does not hold %q", st.Message(), tt.wantText)
			}
		})
	}
}
