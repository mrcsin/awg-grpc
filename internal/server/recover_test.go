package server

import (
	"context"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc/codes"

	awgv1 "github.com/mrcsin/awg-grpc/gen/awg/v1"
	"github.com/mrcsin/awg-grpc/internal/awg"
	"github.com/mrcsin/awg-grpc/internal/awg/awgtest"
)

// panicRunner panics on its first call and delegates every later call.
type panicRunner struct {
	*awgtest.Runner
	once sync.Once
}

func (p *panicRunner) Run(ctx context.Context, cmd awg.Command) ([]byte, error) {
	p.once.Do(func() { panic("runner exploded") })
	return p.Runner.Run(ctx, cmd)
}

func TestRecoverUnary(t *testing.T) {
	fake := awgtest.NewRunner()
	fake.Respond("awg", dumpArgs, beforeDump, nil)
	var logs syncBuffer
	client := startServer(t, &panicRunner{Runner: fake}, &logs)

	sharedKey := key(0x14)
	req := &awgv1.ApplyPeersRequest{
		InterfaceName: presentIface,
		Peers:         []*awgv1.Peer{reqPeer(0x04, 0x14, "10.8.1.5/32")},
	}
	_, err := client.ApplyPeers(context.Background(), req)
	st := assertCode(t, err, codes.Internal)
	if st.Message() != "internal error" {
		t.Errorf("message = %q, want %q", st.Message(), "internal error")
	}

	logText := logs.String()
	for _, want := range []string{"/awg.v1.ManagementService/ApplyPeers", "runner exploded", "level=ERROR"} {
		if !strings.Contains(logText, want) {
			t.Errorf("log does not hold %q:\n%s", want, logText)
		}
	}
	assertNoKeyText(t, logText, sharedKey)

	// The panic unwound through the interface lock; the next apply on it is served.
	req.DryRun = true
	resp, err := client.ApplyPeers(context.Background(), req)
	if err != nil {
		t.Fatalf("apply after the panic: %v", err)
	}
	assertChanges(t, resp, keyBytes(0x04), keyBytes(0x01, 0x02, 0x03), nil)
}
