package reconcile

import (
	"encoding/base64"
	"reflect"
	"strings"
	"testing"

	"github.com/mrcsin/awg-grpc/internal/awg"
)

func pskFile(b byte) []byte {
	return []byte(key(b).String() + "\n")
}

func TestBuildSet(t *testing.T) {
	tests := []struct {
		name      string
		kernel    []awg.Peer
		desired   []awg.Peer
		wantArgs  []string
		wantFiles [][]byte
	}{
		{
			name: "removals come first",
			kernel: []awg.Peer{
				peer(0x05, psk(0x15), "10.8.1.5/32"),
				peer(0x01, psk(0x11), "10.8.1.2/32"),
			},
			desired: []awg.Peer{
				peer(0x01, psk(0x11), "10.8.1.5/32"),
				peer(0x02, psk(0x12), "10.8.1.2/32"),
			},
			wantArgs: []string{
				"set", "awg0",
				"peer", key(0x05).String(), "remove",
				"peer", key(0x02).String(), "allowed-ips", "10.8.1.2/32", "preshared-key", "/dev/fd/3",
				"peer", key(0x01).String(), "allowed-ips", "10.8.1.5/32",
			},
			wantFiles: [][]byte{pskFile(0x12)},
		},
		{
			name: "added peers always get a preshared key, fds in order",
			desired: []awg.Peer{
				peer(0x02, psk(0x12), "10.8.1.3/32"),
				peer(0x01, psk(0x11), "fd00:8::2/128"),
			},
			wantArgs: []string{
				"set", "awg0",
				"peer", key(0x01).String(), "allowed-ips", "fd00:8::2/128", "preshared-key", "/dev/fd/3",
				"peer", key(0x02).String(), "allowed-ips", "10.8.1.3/32", "preshared-key", "/dev/fd/4",
			},
			wantFiles: [][]byte{pskFile(0x11), pskFile(0x12)},
		},
		{
			name:    "kernel peer without a preshared key gets one",
			kernel:  []awg.Peer{peer(0x01, nil, "10.8.1.2/32")},
			desired: []awg.Peer{peer(0x01, psk(0x11), "10.8.1.2/32")},
			wantArgs: []string{
				"set", "awg0",
				"peer", key(0x01).String(), "allowed-ips", "10.8.1.2/32", "preshared-key", "/dev/fd/3",
			},
			wantFiles: [][]byte{pskFile(0x11)},
		},
		{
			name:    "changed preshared key is set",
			kernel:  []awg.Peer{peer(0x01, psk(0x11), "10.8.1.2/32")},
			desired: []awg.Peer{peer(0x01, psk(0x21), "10.8.1.2/32")},
			wantArgs: []string{
				"set", "awg0",
				"peer", key(0x01).String(), "allowed-ips", "10.8.1.2/32", "preshared-key", "/dev/fd/3",
			},
			wantFiles: [][]byte{pskFile(0x21)},
		},
		{
			name:    "updated peer with an unchanged preshared key has no preshared-key",
			kernel:  []awg.Peer{peer(0x01, psk(0x11), "10.8.1.2/32", "10.8.1.3/32")},
			desired: []awg.Peer{peer(0x01, psk(0x11), "10.8.1.64/26")},
			wantArgs: []string{
				"set", "awg0",
				"peer", key(0x01).String(), "allowed-ips", "10.8.1.64/26",
			},
		},
		{
			name: "removals only pass no extra files",
			kernel: []awg.Peer{
				peer(0x02, psk(0x12), "10.8.1.3/32"),
				peer(0x01, nil, "10.8.1.2/32"),
			},
			wantArgs: []string{
				"set", "awg0",
				"peer", key(0x01).String(), "remove",
				"peer", key(0x02).String(), "remove",
			},
		},
		{
			name: "unchanged peers are left out",
			kernel: []awg.Peer{
				peer(0x01, psk(0x11), "10.8.1.2/32"),
				peer(0x02, psk(0x12), "10.8.1.3/32"),
			},
			desired: []awg.Peer{
				peer(0x01, psk(0x11), "10.8.1.2/32"),
				peer(0x02, psk(0x22), "10.8.1.3/32"),
				peer(0x03, psk(0x13), "10.8.1.4/32"),
			},
			wantArgs: []string{
				"set", "awg0",
				"peer", key(0x03).String(), "allowed-ips", "10.8.1.4/32", "preshared-key", "/dev/fd/3",
				"peer", key(0x02).String(), "allowed-ips", "10.8.1.3/32", "preshared-key", "/dev/fd/4",
			},
			wantFiles: [][]byte{pskFile(0x13), pskFile(0x22)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changes := Diff(tt.kernel, tt.desired)
			cmd, ok := BuildSet("awg0", changes, tt.desired, tt.kernel)
			if !ok {
				t.Fatal("BuildSet returned false for a non-empty diff")
			}
			if cmd.Name != "awg" {
				t.Errorf("Name = %q, want %q", cmd.Name, "awg")
			}
			if !reflect.DeepEqual(cmd.Args, tt.wantArgs) {
				t.Errorf("Args:\n got %q\nwant %q", cmd.Args, tt.wantArgs)
			}
			if !reflect.DeepEqual(cmd.ExtraFiles, tt.wantFiles) {
				t.Errorf("ExtraFiles:\n got %q\nwant %q", cmd.ExtraFiles, tt.wantFiles)
			}
			assertArgsHideKeys(t, cmd.Args, tt.kernel, tt.desired)
			assertCIDRsCanonical(t, cmd.Args, tt.desired)
		})
	}
}

func TestBuildSetEmptyChanges(t *testing.T) {
	peers := []awg.Peer{peer(0x01, psk(0x11), "10.8.1.2/32")}
	tests := []struct {
		name    string
		changes Changes
	}{
		{name: "zero value", changes: Changes{}},
		{name: "diff of identical sets", changes: Diff(peers, peers)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, ok := BuildSet("awg0", tt.changes, peers, peers)
			if ok {
				t.Fatalf("BuildSet returned true with %q", cmd.Args)
			}
			if !reflect.DeepEqual(cmd, awg.Command{}) {
				t.Errorf("command = %+v, want the zero value", cmd)
			}
		})
	}
}

func assertArgsHideKeys(t *testing.T, args []string, peerSets ...[]awg.Peer) {
	t.Helper()
	joined := strings.Join(args, " ")
	for _, peers := range peerSets {
		for _, p := range peers {
			if p.PresharedKey != nil {
				assertNoKeyText(t, joined, p.PresharedKey.Bytes())
			}
		}
	}
}

// assertCIDRsCanonical checks that every allowed-ips value is the prefix String form of the
// desired peer it belongs to.
func assertCIDRsCanonical(t *testing.T, args []string, desired []awg.Peer) {
	t.Helper()
	wantByKey := byPublicKey(desired)
	for i := 0; i+1 < len(args); i++ {
		if args[i] != "allowed-ips" {
			continue
		}
		pub, err := base64.StdEncoding.DecodeString(args[i-1])
		if err != nil || len(pub) != 32 {
			t.Fatalf("argument before allowed-ips %q is not a public key", args[i-1])
		}
		p, ok := wantByKey[awg.Key(pub)]
		if !ok {
			t.Fatalf("allowed-ips for a key outside the desired set: %s", args[i-1])
		}
		if want := strings.Join(awg.PrefixStrings(p.AllowedIPs), ","); args[i+1] != want {
			t.Errorf("allowed-ips %q for %s, want %q", args[i+1], args[i-1], want)
		}
	}
}
