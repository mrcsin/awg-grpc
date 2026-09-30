package reconcile

import (
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"strings"
	"testing"

	awgv1 "github.com/mrcsin/awg-grpc/gen/awg/v1"
	"github.com/mrcsin/awg-grpc/internal/awg"
	"github.com/mrcsin/awg-grpc/internal/awg/awgtest"
)

func key(b byte) awg.Key {
	var k awg.Key
	for i := range k {
		k[i] = b
	}
	return k
}

func psk(b byte) *awg.PresharedKey {
	return awg.NewPresharedKey(key(b))
}

func reqPeer(pub, sharedKey byte, cidr string) *awgv1.Peer {
	pk := key(pub)
	sk := key(sharedKey)
	return &awgv1.Peer{PublicKey: pk[:], PresharedKey: sk[:], AllowedIp: cidr}
}

func testInterface() Interface {
	return Interface{
		PublicKey: key(0xEE),
		Addresses: []netip.Prefix{
			netip.MustParsePrefix("10.8.1.1/24"),
			netip.MustParsePrefix("10.9.0.1/16"),
			netip.MustParsePrefix("fd00:8::1/64"),
		},
	}
}

func TestDesiredValid(t *testing.T) {
	tests := []struct {
		name string
		req  *awgv1.ApplyPeersRequest
		want []awg.Peer
	}{
		{
			name: "peers convert in request order",
			req: &awgv1.ApplyPeersRequest{Peers: []*awgv1.Peer{
				reqPeer(0x02, 0x12, "10.8.1.2/32"),
				reqPeer(0x01, 0x11, "10.8.1.64/26"),
				reqPeer(0x03, 0x13, "10.9.0.3/32"),
			}},
			want: []awg.Peer{
				{PublicKey: key(0x02), PresharedKey: psk(0x12), AllowedIPs: []netip.Prefix{netip.MustParsePrefix("10.8.1.2/32")}},
				{PublicKey: key(0x01), PresharedKey: psk(0x11), AllowedIPs: []netip.Prefix{netip.MustParsePrefix("10.8.1.64/26")}},
				{PublicKey: key(0x03), PresharedKey: psk(0x13), AllowedIPs: []netip.Prefix{netip.MustParsePrefix("10.9.0.3/32")}},
			},
		},
		{
			name: "empty list with allow_empty converts to no peers",
			req:  &awgv1.ApplyPeersRequest{AllowEmpty: true},
			want: []awg.Peer{},
		},
		{
			name: "MaxPeers peers are accepted",
			req:  &awgv1.ApplyPeersRequest{Peers: manyPeers(MaxPeers)},
			want: nil, // length checked below
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Desired(tt.req, testInterface())
			if err != nil {
				t.Fatalf("Desired: %v", err)
			}
			if tt.want == nil {
				if len(got) != len(tt.req.Peers) {
					t.Fatalf("got %d peers, want %d", len(got), len(tt.req.Peers))
				}
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

// manyPeers returns n valid peers with distinct keys and /32 addresses inside 10.9.0.0/16, from
// 10.9.0.2 on.
func manyPeers(n int) []*awgv1.Peer {
	peers := make([]*awgv1.Peer, n)
	for i := range peers {
		var pub, shared [32]byte
		pub[0], pub[1], pub[31] = byte(i>>8), byte(i), 0x01
		shared[0], shared[1], shared[31] = byte(i>>8), byte(i), 0x02
		peers[i] = &awgv1.Peer{
			PublicKey:    pub[:],
			PresharedKey: shared[:],
			AllowedIp:    fmt.Sprintf("10.9.%d.%d/32", (i+2)>>8, (i+2)&0xff),
		}
	}
	return peers
}

func TestDesiredRejects(t *testing.T) {
	shortPSK := []byte("0123456789abcdefghijklmnopqrstu") // 31 bytes
	own := key(0xEE)

	// inMessage holds text only the expected rule writes, and "peer <index>: " for a peer rule.
	tests := []struct {
		name      string
		req       *awgv1.ApplyPeersRequest
		inMessage []string
	}{
		{
			name:      "empty list without allow_empty",
			req:       &awgv1.ApplyPeersRequest{},
			inMessage: []string{"peer list is empty; set allow_empty"},
		},
		{
			name: "public key of 31 bytes",
			req: &awgv1.ApplyPeersRequest{Peers: []*awgv1.Peer{
				reqPeer(0x01, 0x11, "10.8.1.2/32"),
				{PublicKey: make([]byte, 31), PresharedKey: keySlice(0x12), AllowedIp: "10.8.1.3/32"},
			}},
			inMessage: []string{"peer 1: public key is 31 bytes"},
		},
		{
			name: "all-zero public key",
			req: &awgv1.ApplyPeersRequest{Peers: []*awgv1.Peer{
				reqPeer(0x00, 0x11, "10.8.1.2/32"),
			}},
			inMessage: []string{"peer 0: public key is all zeros"},
		},
		{
			name: "public key equals the interface key",
			req: &awgv1.ApplyPeersRequest{Peers: []*awgv1.Peer{
				reqPeer(0x01, 0x11, "10.8.1.2/32"),
				{PublicKey: own[:], PresharedKey: keySlice(0x12), AllowedIp: "10.8.1.3/32"},
			}},
			inMessage: []string{"peer 1: public key " + own.String() + " equals the interface public key"},
		},
		{
			name: "duplicate public key",
			req: &awgv1.ApplyPeersRequest{Peers: []*awgv1.Peer{
				reqPeer(0x01, 0x11, "10.8.1.2/32"),
				reqPeer(0x02, 0x12, "10.8.1.3/32"),
				reqPeer(0x01, 0x13, "10.8.1.4/32"),
			}},
			inMessage: []string{"peer 2: public key " + key(0x01).String() + " repeats peer 0"},
		},
		{
			name: "preshared key of 31 bytes",
			req: &awgv1.ApplyPeersRequest{Peers: []*awgv1.Peer{
				{PublicKey: keySlice(0x01), PresharedKey: shortPSK, AllowedIp: "10.8.1.2/32"},
			}},
			inMessage: []string{"peer 0: " + key(0x01).String() + ": preshared key is 31 bytes"},
		},
		{
			name: "missing preshared key",
			req: &awgv1.ApplyPeersRequest{Peers: []*awgv1.Peer{
				{PublicKey: keySlice(0x01), AllowedIp: "10.8.1.2/32"},
			}},
			inMessage: []string{"peer 0: ", "preshared key is 0 bytes"},
		},
		{
			name: "all-zero preshared key",
			req: &awgv1.ApplyPeersRequest{Peers: []*awgv1.Peer{
				reqPeer(0x01, 0x00, "10.8.1.2/32"),
			}},
			inMessage: []string{"peer 0: ", "preshared key is all zeros"},
		},
		{
			name: "allowed_ip is not a CIDR",
			req: &awgv1.ApplyPeersRequest{Peers: []*awgv1.Peer{
				reqPeer(0x01, 0x11, "10.8.1.2"),
			}},
			inMessage: []string{"peer 0: ", `allowed_ip "10.8.1.2" is not a CIDR`},
		},
		{
			name: "allowed_ip with a newline is shown escaped",
			req: &awgv1.ApplyPeersRequest{Peers: []*awgv1.Peer{
				reqPeer(0x01, 0x11, "10.8.1.2/32\nforged log line"),
			}},
			inMessage: []string{`allowed_ip "10.8.1.2/32\nforged log line" is not a CIDR`},
		},
		{
			name: "allowed_ip with host bits set",
			req: &awgv1.ApplyPeersRequest{Peers: []*awgv1.Peer{
				reqPeer(0x01, 0x11, "10.8.1.7/24"),
			}},
			inMessage: []string{"peer 0: ", "allowed_ip 10.8.1.7/24 has host bits set, want 10.8.1.0/24"},
		},
		{
			name: "IPv6 allowed_ip inside an IPv6 interface network",
			req: &awgv1.ApplyPeersRequest{Peers: []*awgv1.Peer{
				reqPeer(0x01, 0x11, "fd00:8::3/128"),
			}},
			inMessage: []string{"peer 0: ", "allowed_ip fd00:8::3/128 is not IPv4"},
		},
		{
			name: "IPv4-mapped IPv6 allowed_ip",
			req: &awgv1.ApplyPeersRequest{Peers: []*awgv1.Peer{
				reqPeer(0x01, 0x11, "::ffff:10.8.1.2/128"),
			}},
			inMessage: []string{"peer 0: ", "allowed_ip ::ffff:10.8.1.2/128 is not IPv4"},
		},
		{
			name: "allowed_ip outside the interface addresses",
			req: &awgv1.ApplyPeersRequest{Peers: []*awgv1.Peer{
				reqPeer(0x01, 0x11, "10.10.0.2/32"),
			}},
			inMessage: []string{"peer 0: ", "allowed_ip 10.10.0.2/32 lies outside the interface addresses"},
		},
		{
			name: "allowed_ip wider than the interface prefix",
			req: &awgv1.ApplyPeersRequest{Peers: []*awgv1.Peer{
				reqPeer(0x01, 0x11, "10.8.0.0/16"),
			}},
			inMessage: []string{"peer 0: ", "allowed_ip 10.8.0.0/16 lies outside the interface addresses"},
		},
		{
			name: "allowed_ip equals an interface address",
			req: &awgv1.ApplyPeersRequest{Peers: []*awgv1.Peer{
				reqPeer(0x01, 0x11, "10.8.1.1/32"),
			}},
			inMessage: []string{"peer 0: ", "allowed_ip 10.8.1.1/32 contains the interface address 10.8.1.1"},
		},
		{
			name: "allowed_ip covers the interface subnet",
			req: &awgv1.ApplyPeersRequest{Peers: []*awgv1.Peer{
				reqPeer(0x01, 0x11, "10.9.0.0/16"),
			}},
			inMessage: []string{"peer 0: ", "allowed_ip 10.9.0.0/16 contains the interface address 10.9.0.1"},
		},
		{
			name: "overlapping allowed_ip values",
			req: &awgv1.ApplyPeersRequest{Peers: []*awgv1.Peer{
				reqPeer(0x01, 0x11, "10.8.1.64/26"),
				reqPeer(0x02, 0x12, "10.8.1.2/32"),
				reqPeer(0x03, 0x13, "10.8.1.70/32"),
			}},
			inMessage: []string{"peer 2: ", "allowed_ip 10.8.1.70/32 overlaps 10.8.1.64/26 of peer 0"},
		},
		{
			name:      "MaxPeers + 1 peers",
			req:       &awgv1.ApplyPeersRequest{Peers: manyPeers(MaxPeers + 1)},
			inMessage: []string{"peer list holds 1001 peers, the limit is 1000"},
		},
		{
			name:      "allow_empty does not lift the limit",
			req:       &awgv1.ApplyPeersRequest{Peers: manyPeers(MaxPeers + 1), AllowEmpty: true},
			inMessage: []string{"peer list holds 1001 peers, the limit is 1000"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Desired(tt.req, testInterface())
			if err == nil {
				t.Fatalf("Desired returned %d peers and no error", len(got))
			}
			var invalid *ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("error %v is %T, want *ValidationError", err, err)
			}
			msg := err.Error()
			if strings.ContainsAny(msg, "\n\r") {
				t.Fatalf("message holds a line break: %q", msg)
			}
			for _, s := range tt.inMessage {
				if !strings.Contains(msg, s) {
					t.Errorf("message %q lacks %q", msg, s)
				}
			}
			for i, p := range tt.req.Peers {
				if i > 3 {
					break
				}
				assertNoKeyText(t, msg, p.GetPresharedKey())
			}
		})
	}
}

func TestDesiredMessageHidesPresharedKey(t *testing.T) {
	shortPSK := []byte("0123456789abcdefghijklmnopqrstu")
	req := &awgv1.ApplyPeersRequest{Peers: []*awgv1.Peer{
		{PublicKey: keySlice(0x01), PresharedKey: shortPSK, AllowedIp: "10.8.1.2/32"},
	}}
	_, err := Desired(req, testInterface())
	if err == nil {
		t.Fatal("Desired accepted a 31-byte preshared key")
	}
	assertNoKeyText(t, err.Error(), shortPSK)
	assertNoKeyText(t, fmt.Sprintf("%+v", err), shortPSK)
}

func assertNoKeyText(t *testing.T, msg string, k []byte) {
	t.Helper()
	if len(k) == 0 {
		return
	}
	for _, f := range awgtest.KeyForms(k) {
		if strings.Contains(msg, f) {
			t.Fatalf("message %q holds preshared key text %q", msg, f)
		}
	}
}

func keySlice(b byte) []byte {
	k := key(b)
	return k[:]
}
