//go:build smoke

package smoke

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	awgv1 "github.com/mrcsin/awg-grpc/gen/awg/v1"
)

// clientParamCount is the number of keys of deploy/smoke/awgsmoke0.conf that GetStatus returns in
// client_params: every 3.1 parameter. A config that loses one fails the status subtest.
const clientParamCount = 25

// toolsVersion is the amneziawg-tools version the image builds. The host module must report the
// same version; the CI kernel job builds the module that AWG_MODULE_REF in ci.yml pins.
const toolsVersion = "3.1.20260812"

// TestSmoke runs its subtests in order; each one starts from the state the previous one left, and
// the destructive ones come last.
func TestSmoke(t *testing.T) {
	client := dial(t)
	steps := []struct {
		name string
		run  func(t *testing.T, client awgv1.ManagementServiceClient)
	}{
		{"status", testStatus},
		{"handshake", testHandshake},
		{"restart", testRestart},
		{"interface loss", testInterfaceLoss},
		{"stop", testStop},
	}
	for _, step := range steps {
		if !t.Run(step.name, func(t *testing.T) { step.run(t, client) }) {
			t.FailNow()
		}
	}
}

func testStatus(t *testing.T, client awgv1.ManagementServiceClient) {
	resp, err := client.GetStatus(callContext(t), &awgv1.GetStatusRequest{}, waitForReady)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if resp.GetToolsVersion() != toolsVersion || resp.GetModuleVersion() != toolsVersion {
		t.Fatalf("tools_version = %q, module_version = %q, want both %q",
			resp.GetToolsVersion(), resp.GetModuleVersion(), toolsVersion)
	}
	iface := getStatus(t, client)
	if !iface.GetPresent() {
		t.Fatalf("%s present = false, want true", smokeIface)
	}
	var got []string
	for _, p := range iface.GetClientParams() {
		got = append(got, p.GetKey())
	}
	want := clientParamKeys(t)
	if len(want) != clientParamCount {
		t.Fatalf("%s.conf holds %d client keys %q, want %d", smokeIface, len(want), want, clientParamCount)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("client_params: %d keys %q, want %d keys %q", len(got), got, len(want), want)
	}
}

// clientParamKeys returns the sorted keys of deploy/smoke/awgsmoke0.conf without the ones
// GetStatus never returns in client_params.
func clientParamKeys(t *testing.T) []string {
	t.Helper()
	serverOnlyKeys := []string{"PrivateKey", "Address", "ListenPort", "PostUp", "PostDown"}
	content, err := os.ReadFile(filepath.Join(repoDir, "deploy", "smoke", smokeIface+".conf"))
	if err != nil {
		t.Fatalf("reading smoke config: %v", err)
	}
	var keys []string
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		key, _, ok := strings.Cut(line, "=")
		if !ok || strings.HasPrefix(line, "#") {
			continue
		}
		key = strings.TrimSpace(key)
		if !slices.Contains(serverOnlyKeys, key) {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return keys
}

// testRestart proves that a container restart starts from the config file: the peers applied
// before it are gone after it.
func testRestart(t *testing.T, client awgv1.ManagementServiceClient) {
	first, _ := newPeer(t, "10.99.0.3/32")
	second, _ := newPeer(t, "10.99.0.4/32")
	applyPeers(t, client, first, second)
	if n := getStatus(t, client).GetPeerCount(); n != 2 {
		t.Fatalf("peer_count before restart = %d, want 2", n)
	}

	compose(t, "restart", "awg")
	waitHealth(t, "awg", "healthy", 60*time.Second)

	iface := getStatus(t, client)
	if !iface.GetPresent() {
		t.Fatalf("%s present = false after restart, want true", smokeIface)
	}
	if n := iface.GetPeerCount(); n != 0 {
		t.Fatalf("peer_count after restart = %d, want 0", n)
	}
}

func testInterfaceLoss(t *testing.T, client awgv1.ManagementServiceClient) {
	compose(t, "exec", "-T", "awg", "ip", "link", "del", smokeIface)

	if getStatus(t, client).GetPresent() {
		t.Fatalf("%s present = true after ip link del, want false", smokeIface)
	}

	stdout, stderr, err := run(composeCmd("exec", "-T", "awg", "awg-grpc", "healthcheck"))
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("healthcheck: %v, want exit code 1\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if want := "healthcheck: NOT_SERVING"; !strings.Contains(stderr, want) {
		t.Fatalf("healthcheck stderr = %q, want %q", stderr, want)
	}
	waitHealth(t, "awg", "unhealthy", 60*time.Second)
}

func testStop(t *testing.T, _ awgv1.ManagementServiceClient) {
	compose(t, "stop", "awg")
	if code := inspect(t, "awg", "{{.State.ExitCode}}"); code != "0" {
		t.Fatalf("awg exit code = %s, want 0", code)
	}
}

// newPeer returns a peer with a real X25519 public key and a random preshared key, and the private
// key of that peer.
func newPeer(t *testing.T, cidr string) (*awgv1.Peer, *ecdh.PrivateKey) {
	t.Helper()
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	psk := make([]byte, 32)
	_, _ = rand.Read(psk)
	peer := &awgv1.Peer{PublicKey: bytes.Clone(priv.PublicKey().Bytes()), PresharedKey: psk, AllowedIp: cidr}
	return peer, priv
}
