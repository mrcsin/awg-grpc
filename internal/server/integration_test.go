//go:build integration

package server

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	awgv1 "github.com/mrcsin/awg-grpc/gen/awg/v1"
	"github.com/mrcsin/awg-grpc/internal/awg"
	"github.com/mrcsin/awg-grpc/internal/node"
	"github.com/mrcsin/awg-grpc/internal/reconcile"
)

// The integration suite runs in the integration image stage with NET_ADMIN against the host
// amneziawg module. It brings up itIface from the fixture config and deletes it at the end.
const (
	itIface           = "awgit0"
	moduleVersionFile = "/sys/module/amneziawg/version"
)

var itConfigDir = filepath.Join("..", "..", "testdata", "it")

// itPeer is one desired peer with a real X25519 public key and a random preshared key. Peer n
// holds 10.77.(1+n/250).(1+n%250)/32, inside the fixture's 10.77.0.1/16 and never its address.
type itPeer struct {
	pub  awg.Key
	psk  awg.Key
	cidr string
}

func newITPeer(t *testing.T, n int) itPeer {
	t.Helper()
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	p := itPeer{psk: randomKey(), cidr: peerCIDR(n)}
	copy(p.pub[:], priv.PublicKey().Bytes())
	return p
}

func newITPeers(t *testing.T, first, count int) []itPeer {
	t.Helper()
	peers := make([]itPeer, 0, count)
	for n := first; n < first+count; n++ {
		peers = append(peers, newITPeer(t, n))
	}
	return peers
}

func randomKey() awg.Key {
	var k awg.Key
	_, _ = rand.Read(k[:])
	return k
}

func peerCIDR(n int) string {
	return fmt.Sprintf("10.77.%d.%d/32", 1+n/250, 1+n%250)
}

func (p itPeer) message() *awgv1.Peer {
	return &awgv1.Peer{PublicKey: bytes.Clone(p.pub[:]), PresharedKey: bytes.Clone(p.psk[:]), AllowedIp: p.cidr}
}

func messages(peers []itPeer) []*awgv1.Peer {
	out := make([]*awgv1.Peer, 0, len(peers))
	for _, p := range peers {
		out = append(out, p.message())
	}
	return out
}

// sortedKeys returns the public keys in the order ApplyPeersResponse lists them.
func sortedKeys(peers ...itPeer) [][]byte {
	keys := make([][]byte, 0, len(peers))
	for _, p := range peers {
		keys = append(keys, bytes.Clone(p.pub[:]))
	}
	slices.SortFunc(keys, bytes.Compare)
	return keys
}

// startIT runs the production startup, node.Start, on the fixture directory and serves
// NewGRPCServer on a temp unix socket. Cleanup stops the server, then deletes itIface.
func startIT(t *testing.T) awgv1.ManagementServiceClient {
	t.Helper()
	runner := awg.ExecRunner{}

	t.Cleanup(func() {
		if _, present, _ := NetLookup(itIface); !present {
			return
		}
		if out, err := exec.Command("ip", "link", "del", itIface).CombinedOutput(); err != nil {
			t.Errorf("ip link del %s: %v: %s", itIface, err, out)
		}
	})
	names, toolsVersion, err := node.Start(t.Context(), runner, NetLookup, itConfigDir)
	if err != nil {
		t.Fatalf("node.Start: %v", err)
	}
	if !slices.Equal(names, []string{itIface}) {
		t.Fatalf("node.Start interfaces = %q, want only %s", names, itIface)
	}

	socket := filepath.Join(t.TempDir(), "awg.sock")
	lis, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(t.Output(), nil))
	srv := NewGRPCServer(runner, NetLookup, names, Versions{
		Wrapper:           "integration",
		Tools:             toolsVersion,
		ModuleVersionFile: moduleVersionFile,
	}, logger)
	served := make(chan error, 1)
	go func() { served <- srv.Serve(lis) }()
	t.Cleanup(func() {
		srv.GracefulStop()
		if err := <-served; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})

	conn, err := grpc.NewClient("unix://"+socket, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return awgv1.NewManagementServiceClient(conn)
}

// awgShow returns the output of awg show itIface <args>, read outside the service.
func awgShow(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("awg", append([]string{"show", itIface}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("awg show %s %s: %v: %s", itIface, strings.Join(args, " "), err, stderr.String())
	}
	return string(out)
}

// keyColumns parses "<public key>\t<value>" lines, as awg show prints preshared-keys.
func keyColumns(t *testing.T, out string) map[string]string {
	t.Helper()
	m := make(map[string]string)
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		pub, value, ok := strings.Cut(sc.Text(), "\t")
		if !ok {
			t.Fatalf("unexpected awg show line %q", sc.Text())
		}
		m[pub] = value
	}
	return m
}

func interfacePublicKey(t *testing.T) awg.Key {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(awgShow(t, "public-key")))
	if err != nil || len(raw) != 32 {
		t.Fatalf("interface public key: %d bytes, %v", len(raw), err)
	}
	return awg.Key(raw)
}

func fixtureInterface(t *testing.T) map[string]string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(itConfigDir, itIface+".conf"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	pairs := make(map[string]string)
	for _, line := range strings.Split(string(content), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		pairs[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return pairs
}

func applyIT(ctx context.Context, client awgv1.ManagementServiceClient, peers []itPeer) (*awgv1.ApplyPeersResponse, error) {
	return client.ApplyPeers(ctx, &awgv1.ApplyPeersRequest{
		InterfaceName: itIface,
		Peers:         messages(peers),
		AllowEmpty:    len(peers) == 0,
	})
}

func setPeers(t *testing.T, client awgv1.ManagementServiceClient, peers ...itPeer) *awgv1.ApplyPeersResponse {
	t.Helper()
	resp, err := applyIT(t.Context(), client, peers)
	if err != nil {
		t.Fatalf("ApplyPeers: %v", err)
	}
	return resp
}

func assertRejected(t *testing.T, client awgv1.ManagementServiceClient, req *awgv1.ApplyPeersRequest) {
	t.Helper()
	before := awgShow(t, "dump")
	_, err := client.ApplyPeers(t.Context(), req)
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Fatalf("ApplyPeers code = %v (%v), want InvalidArgument", code, err)
	}
	if after := awgShow(t, "dump"); after != before {
		t.Errorf("kernel dump changed by a rejected apply:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestIntegration(t *testing.T) {
	client := startIT(t)
	fixture := fixtureInterface(t)

	t.Run("private key unchanged by apply", func(t *testing.T) {
		peers := newITPeers(t, 0, 3)
		kept, gone, added := peers[0], peers[1], peers[2]
		setPeers(t, client, kept, gone)
		before := awgShow(t, "private-key")

		kept.psk = randomKey()
		resp := setPeers(t, client, kept, added)

		assertChanges(t, resp, sortedKeys(added), sortedKeys(gone), sortedKeys(kept))
		if after := awgShow(t, "private-key"); after != before {
			t.Errorf("private key changed by ApplyPeers")
		}
		if got := strings.TrimSpace(before); got != fixture["PrivateKey"] {
			t.Errorf("kernel private key differs from the fixture key")
		}
	})

	t.Run("dry run matches the real apply", func(t *testing.T) {
		peers := newITPeers(t, 10, 5)
		gone, newPSK, newIP, added := peers[0], peers[1], peers[2], peers[3]
		setPeers(t, client, gone, newPSK, newIP)
		newPSK.psk = randomKey()
		newIP.cidr = peers[4].cidr
		req := &awgv1.ApplyPeersRequest{InterfaceName: itIface, Peers: messages([]itPeer{newPSK, newIP, added})}

		before := awgShow(t, "dump")
		dryReq := proto.CloneOf(req)
		dryReq.DryRun = true
		dry, err := client.ApplyPeers(t.Context(), dryReq)
		if err != nil {
			t.Fatalf("dry run: %v", err)
		}
		if after := awgShow(t, "dump"); after != before {
			t.Errorf("kernel dump changed by the dry run:\nbefore:\n%s\nafter:\n%s", before, after)
		}
		applied, err := client.ApplyPeers(t.Context(), req)
		if err != nil {
			t.Fatalf("real apply: %v", err)
		}
		if !proto.Equal(dry, applied) {
			t.Errorf("dry run = %v, real apply = %v", dry, applied)
		}
		assertChanges(t, applied, sortedKeys(added), sortedKeys(gone), sortedKeys(newPSK, newIP))
	})

	t.Run("kernel peer without psk gets the desired psk", func(t *testing.T) {
		p := newITPeer(t, 20)
		setPeers(t, client)
		pub := p.pub.String()
		if out, err := exec.Command("awg", "set", itIface, "peer", pub, "allowed-ips", p.cidr).CombinedOutput(); err != nil {
			t.Fatalf("awg set peer without psk: %v: %s", err, out)
		}
		if got := keyColumns(t, awgShow(t, "preshared-keys"))[pub]; got != "(none)" {
			t.Fatalf("before apply the kernel peer PSK is %q, want (none)", got)
		}

		resp := setPeers(t, client, p)

		assertChanges(t, resp, nil, nil, sortedKeys(p))
		if got := keyColumns(t, awgShow(t, "preshared-keys"))[pub]; got != p.psk.String() {
			t.Errorf("after apply the kernel peer PSK differs from the desired one")
		}
	})

	t.Run("interface key rejected", func(t *testing.T) {
		peers := newITPeers(t, 40, 2)
		setPeers(t, client, peers[0])
		own := peers[1]
		own.pub = interfacePublicKey(t)
		assertRejected(t, client, &awgv1.ApplyPeersRequest{
			InterfaceName: itIface,
			Peers:         messages([]itPeer{peers[0], own}),
		})
	})

	t.Run("empty list with allow_empty removes every peer", func(t *testing.T) {
		peers := newITPeers(t, 60, 2)
		setPeers(t, client, peers...)

		resp := setPeers(t, client)

		assertChanges(t, resp, nil, sortedKeys(peers...), nil)
		list, err := client.ListPeers(t.Context(), &awgv1.ListPeersRequest{InterfaceName: itIface})
		if err != nil {
			t.Fatalf("ListPeers: %v", err)
		}
		if n := len(list.GetPeers()); n != 0 {
			t.Errorf("ListPeers returned %d peers, want 0", n)
		}
	})

	t.Run("apply of MaxPeers peers with psk", func(t *testing.T) {
		setPeers(t, client)
		peers := newITPeers(t, 0, reconcile.MaxPeers)

		start := time.Now()
		resp := setPeers(t, client, peers...)
		elapsed := time.Since(start)

		assertChanges(t, resp, sortedKeys(peers...), nil, nil)
		list, err := client.ListPeers(t.Context(), &awgv1.ListPeersRequest{InterfaceName: itIface})
		if err != nil {
			t.Fatalf("ListPeers: %v", err)
		}
		if n := len(list.GetPeers()); n != reconcile.MaxPeers {
			t.Errorf("ListPeers returned %d peers, want %d", n, reconcile.MaxPeers)
		}
		kernelPSKs := keyColumns(t, awgShow(t, "preshared-keys"))
		for i, p := range peers {
			if kernelPSKs[p.pub.String()] != p.psk.String() {
				t.Fatalf("peer %d: kernel PSK differs from the desired one", i)
			}
		}
		t.Logf("%d-peer apply took %v", len(peers), elapsed.Round(time.Millisecond))
	})

	t.Run("GetStatus reports the interface", func(t *testing.T) {
		resp, err := client.GetStatus(t.Context(), &awgv1.GetStatusRequest{})
		if err != nil {
			t.Fatalf("GetStatus: %v", err)
		}
		if len(resp.GetInterfaces()) != 1 {
			t.Fatalf("GetStatus returned %d interfaces, want 1", len(resp.GetInterfaces()))
		}
		st := resp.GetInterfaces()[0]
		if st.GetName() != itIface || !st.GetPresent() {
			t.Errorf("interface = %q present %v, want %q present true", st.GetName(), st.GetPresent(), itIface)
		}
		if want := []string{fixture["Address"]}; !slices.Equal(st.GetAddresses(), want) {
			t.Errorf("addresses = %v, want %v", st.GetAddresses(), want)
		}
		if pub := interfacePublicKey(t); !bytes.Equal(st.GetPublicKey(), pub[:]) {
			t.Errorf("public key differs from awg show public-key")
		}
		if want := fixture["ListenPort"]; fmt.Sprint(st.GetListenPort()) != want {
			t.Errorf("listen port = %d, want %s", st.GetListenPort(), want)
		}

		// The fixture leaves some keys at their defaults, which showconf prints as 0 or off.
		wantParams := make(map[string]string, len(fixture))
		var defaults []string
		for k, v := range fixture {
			switch {
			case k == "PrivateKey" || k == "Address" || k == "ListenPort":
			case v == "0" || v == "off":
				defaults = append(defaults, k)
			default:
				wantParams[k] = v
			}
		}
		if len(defaults) == 0 {
			t.Fatal("the fixture sets no key to 0 or off, so the filter goes untested")
		}
		gotParams := make(map[string]string, len(st.GetClientParams()))
		for _, p := range st.GetClientParams() {
			if p.GetValue() == "0" || p.GetValue() == "off" {
				t.Errorf("client param %s = %q passed the 0/off filter", p.GetKey(), p.GetValue())
			}
			gotParams[p.GetKey()] = p.GetValue()
		}
		for k, want := range wantParams {
			if got, ok := gotParams[k]; !ok || got != want {
				t.Errorf("client param %s = %q (present %v), want %q", k, got, ok, want)
			}
		}
		for k := range gotParams {
			if _, ok := wantParams[k]; !ok {
				t.Errorf("unexpected client param %s", k)
			}
		}
		for _, k := range defaults {
			if v, ok := gotParams[k]; ok {
				t.Errorf("client param %s = %q is at its default and must be dropped", k, v)
			}
		}
		t.Logf("versions: wrapper %q, tools %q, module %q; %d client params",
			resp.GetWrapperVersion(), resp.GetToolsVersion(), resp.GetModuleVersion(), len(gotParams))
	})
}
