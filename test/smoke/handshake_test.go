//go:build smoke

package smoke

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	awgv1 "github.com/mrcsin/awg-grpc/gen/awg/v1"
)

const (
	clientIface   = "awgc0"
	clientCIDR    = "10.99.0.2/32"
	clientPort    = 51998
	serverTunnel  = "10.99.0.1"
	serverAddress = "awg:51999"

	handshakeTimeout = 30 * time.Second
)

// testHandshake proves that GetStatus.client_params, plus the peer the wrapper applied, give a
// client config that completes a handshake with the smoke interface from a second container and
// then carries pings through the tunnel both ways. Both ends run the same module and tools, so it
// says nothing about phone apps, amneziawg-go peers or any other peer implementation.
func testHandshake(t *testing.T, client awgv1.ManagementServiceClient) {
	peer, priv := newPeer(t, clientCIDR)
	applyPeers(t, client, peer)
	iface := getStatus(t, client)

	conf := filepath.Join(repoDir, ".smoke", "client", clientIface+".conf")
	if err := os.WriteFile(conf, clientConfig(priv.Bytes(), peer.GetPresharedKey(), iface), 0o600); err != nil {
		t.Fatalf("writing client config: %v", err)
	}

	compose(t, "--profile", clientProfile, "up", "-d", "--wait", "client")
	defer removeClient(t)

	// The first packet into the tunnel starts the handshake; its reply may be lost to it.
	stdout, stderr, err := run(composeCmd("exec", "-T", "client", "ping", "-c", "1", "-W", "5", serverTunnel))
	t.Logf("first ping %s (err %v):\n%s%s", serverTunnel, err, stdout, stderr)

	status := waitHandshake(t, client, peer.GetPublicKey())
	age := time.Since(status.GetLastHandshake().AsTime())
	wantEndpoint := fmt.Sprintf("%s:%d", inspect(t, "client", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}"), clientPort)
	t.Logf("handshake: last_handshake age %s, endpoint %s, rx_bytes %d, tx_bytes %d",
		age.Round(time.Millisecond), status.GetEndpoint(), status.GetRxBytes(), status.GetTxBytes())

	if age < -5*time.Second || age > 60*time.Second {
		t.Errorf("last_handshake is %s old, want within 60s of now", age)
	}
	if status.GetEndpoint() != wantEndpoint {
		t.Errorf("endpoint = %q, want %q", status.GetEndpoint(), wantEndpoint)
	}
	if status.GetRxBytes() == 0 || status.GetTxBytes() == 0 {
		t.Errorf("rx_bytes = %d, tx_bytes = %d, want both > 0", status.GetRxBytes(), status.GetTxBytes())
	}

	stdout, stderr, err = run(composeCmd("exec", "-T", "client", "ping", "-c", "3", "-W", "2", serverTunnel))
	if err != nil {
		t.Fatalf("ping %s through the tunnel: %v\n%s%s", serverTunnel, err, stdout, stderr)
	}
	t.Logf("ping %s through the tunnel:\n%s", serverTunnel, stdout)
}

// clientConfig renders the simplest config that can work: the client's own lines, every
// client_params pair verbatim, and one peer for the server.
func clientConfig(priv, psk []byte, iface *awgv1.InterfaceStatus) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "[Interface]\nPrivateKey = %s\nAddress = %s\nListenPort = %d\n",
		base64.StdEncoding.EncodeToString(priv), clientCIDR, clientPort)
	for _, p := range iface.GetClientParams() {
		fmt.Fprintf(&b, "%s = %s\n", p.GetKey(), p.GetValue())
	}
	fmt.Fprintf(&b, "\n[Peer]\nPublicKey = %s\nPresharedKey = %s\nEndpoint = %s\nAllowedIPs = %s/32\n",
		base64.StdEncoding.EncodeToString(iface.GetPublicKey()), base64.StdEncoding.EncodeToString(psk),
		serverAddress, serverTunnel)
	return b.Bytes()
}

// waitHandshake polls ListPeers every second until the peer with key pub has a handshake.
func waitHandshake(t *testing.T, client awgv1.ManagementServiceClient, pub []byte) *awgv1.PeerStatus {
	t.Helper()
	deadline := time.Now().Add(handshakeTimeout)
	for time.Now().Before(deadline) {
		resp, err := client.ListPeers(callContext(t), &awgv1.ListPeersRequest{InterfaceName: smokeIface}, waitForReady)
		if err != nil {
			t.Fatalf("ListPeers: %v", err)
		}
		for _, p := range resp.GetPeers() {
			if bytes.Equal(p.GetPublicKey(), pub) && p.GetLastHandshake() != nil {
				return p
			}
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("no handshake within %s", handshakeTimeout)
	return nil
}

// removeClient logs the client container when the subtest failed, then removes the container.
func removeClient(t *testing.T) {
	t.Helper()
	if t.Failed() {
		out, err := composeCmd("--profile", clientProfile, "logs", "--no-color", "client").CombinedOutput()
		t.Logf("docker compose logs client (err %v):\n%s", err, out)
	}
	if out, err := composeCmd("--profile", clientProfile, "rm", "-sf", "client").CombinedOutput(); err != nil {
		t.Errorf("removing client: %v\n%s", err, out)
	}
}
