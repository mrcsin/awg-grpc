//go:build smoke

// Package smoke drives the runtime image through deploy/compose.smoke.yml against the host
// amneziawg module. It needs Docker with compose and the module loaded; AGENTS.md, Test, runs it.
package smoke

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	awgv1 "github.com/mrcsin/awg-grpc/gen/awg/v1"
)

const (
	// repoDir is the repository root relative to this package; go test runs in the package dir.
	repoDir       = "../.."
	composeFile   = "deploy/compose.smoke.yml"
	smokeIface    = "awgsmoke0"
	clientProfile = "handshake"
)

func TestMain(m *testing.M) {
	os.Exit(runMain(m))
}

// runMain creates the bind-mount directories as the test user, so dockerd does not create them
// as root, starts the awg service and takes the project down at the end. A run that was killed
// leaves the project up, so runMain also takes it down before it starts.
func runMain(m *testing.M) int {
	flag.Parse()
	if count := flag.Lookup("test.count").Value.String(); count != "1" {
		fmt.Fprintf(os.Stderr, "-count=%s: TestSmoke stops the awg service, so it runs once per process; use -count=1\n", count)
		return 1
	}
	down()
	for _, dir := range []string{".smoke/sock", ".smoke/client"} {
		if err := os.MkdirAll(filepath.Join(repoDir, dir), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "creating %s: %v\n", dir, err)
			return 1
		}
	}
	defer down()

	out, err := composeCmd("up", "-d", "--build", "--wait", "awg").CombinedOutput()
	fmt.Fprintf(os.Stderr, "docker compose up:\n%s\n", out)
	if err != nil {
		fmt.Fprintf(os.Stderr, "docker compose up: %v\n", err)
		printLogs()
		return 1
	}
	code := m.Run()
	if code != 0 {
		printLogs()
	}
	return code
}

func down() {
	out, err := composeCmd("--profile", clientProfile, "down", "--remove-orphans", "--timeout", "10").CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "docker compose down: %v\n%s\n", err, out)
	}
}

func printLogs() {
	out, err := composeCmd("--profile", clientProfile, "logs", "--no-color").CombinedOutput()
	fmt.Fprintf(os.Stderr, "docker compose logs (err %v):\n%s\n", err, out)
}

// composeCmd builds docker compose on the smoke project, run from the repository root with
// SMOKE_GID set to the test user's group, so the test can open the socket.
func composeCmd(args ...string) *exec.Cmd {
	cmd := exec.Command("docker", append([]string{"compose", "-f", composeFile}, args...)...)
	cmd.Dir = repoDir
	cmd.Env = append(os.Environ(), "SMOKE_GID="+strconv.Itoa(os.Getgid()))
	return cmd
}

// run runs cmd and returns its stdout and stderr. The error wraps the exit status.
func run(cmd *exec.Cmd) (stdout, stderr string, err error) {
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	return outBuf.String(), errBuf.String(), err
}

// compose runs docker compose with args, fails the test on a non-zero exit and returns stdout.
func compose(t *testing.T, args ...string) string {
	t.Helper()
	stdout, stderr, err := run(composeCmd(args...))
	if err != nil {
		t.Fatalf("docker compose %s: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, stdout, stderr)
	}
	return stdout
}

// inspect returns docker inspect -f format of the container of service, running or stopped.
func inspect(t *testing.T, service, format string) string {
	t.Helper()
	id := strings.TrimSpace(compose(t, "ps", "-a", "-q", service))
	if id == "" {
		t.Fatalf("service %s has no container", service)
	}
	stdout, stderr, err := run(exec.Command("docker", "inspect", "-f", format, id))
	if err != nil {
		t.Fatalf("docker inspect %s: %v: %s", service, err, stderr)
	}
	return strings.TrimSpace(stdout)
}

// waitHealth polls the compose health state of service every second until it equals want.
func waitHealth(t *testing.T, service, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var got string
	for time.Now().Before(deadline) {
		got = inspect(t, service, "{{.State.Health.Status}}")
		if got == want {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("health of %s = %q after %s, want %q", service, got, timeout, want)
}

// dial connects to the socket the awg service serves in .smoke/sock. grpc needs the absolute
// path in a unix:// target.
func dial(t *testing.T) awgv1.ManagementServiceClient {
	t.Helper()
	socket, err := filepath.Abs(filepath.Join(repoDir, ".smoke", "sock", "awg.sock"))
	if err != nil {
		t.Fatalf("resolving socket path: %v", err)
	}
	conn, err := grpc.NewClient("unix://"+socket, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return awgv1.NewManagementServiceClient(conn)
}

// waitForReady lets a call made right after a restart wait for the new socket instead of failing
// on the old connection. Every RPC of the smoke test passes it.
var waitForReady = grpc.WaitForReady(true)

// callContext bounds one RPC.
func callContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func getStatus(t *testing.T, client awgv1.ManagementServiceClient) *awgv1.InterfaceStatus {
	t.Helper()
	resp, err := client.GetStatus(callContext(t), &awgv1.GetStatusRequest{}, waitForReady)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	for _, iface := range resp.GetInterfaces() {
		if iface.GetName() == smokeIface {
			return iface
		}
	}
	t.Fatalf("GetStatus lists no %s: %v", smokeIface, resp.GetInterfaces())
	return nil
}

func applyPeers(t *testing.T, client awgv1.ManagementServiceClient, peers ...*awgv1.Peer) {
	t.Helper()
	_, err := client.ApplyPeers(callContext(t), &awgv1.ApplyPeersRequest{
		InterfaceName: smokeIface,
		Peers:         peers,
	}, waitForReady)
	if err != nil {
		t.Fatalf("ApplyPeers: %v", err)
	}
}
