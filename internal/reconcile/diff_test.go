package reconcile

import (
	"fmt"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"github.com/mrcsin/awg-grpc/internal/awg"
)

func peer(pub byte, sharedKey *awg.PresharedKey, cidrs ...string) awg.Peer {
	p := awg.Peer{PublicKey: key(pub), PresharedKey: sharedKey}
	for _, c := range cidrs {
		p.AllowedIPs = append(p.AllowedIPs, netip.MustParsePrefix(c))
	}
	return p
}

func keys(bs ...byte) []awg.Key {
	out := make([]awg.Key, 0, len(bs))
	for _, b := range bs {
		out = append(out, key(b))
	}
	return out
}

func TestDiff(t *testing.T) {
	tests := []struct {
		name string
		from []awg.Peer
		to   []awg.Peer
		want Changes
	}{
		{
			name: "both empty",
			want: Changes{Added: keys(), Removed: keys(), Updated: keys()},
		},
		{
			name: "empty from adds every peer",
			to: []awg.Peer{
				peer(0x02, psk(0x12), "10.8.1.2/32"),
				peer(0x01, psk(0x11), "10.8.1.3/32"),
			},
			want: Changes{Added: keys(0x01, 0x02), Removed: keys(), Updated: keys()},
		},
		{
			name: "empty to removes every peer",
			from: []awg.Peer{
				peer(0x02, psk(0x12), "10.8.1.2/32"),
				peer(0x01, nil, "10.8.1.3/32"),
			},
			want: Changes{Added: keys(), Removed: keys(0x01, 0x02), Updated: keys()},
		},
		{
			name: "identical sets give an empty diff",
			from: []awg.Peer{
				peer(0x01, psk(0x11), "10.8.1.2/32"),
				peer(0x02, nil, "10.8.1.3/32"),
			},
			to: []awg.Peer{
				peer(0x02, nil, "10.8.1.3/32"),
				peer(0x01, psk(0x11), "10.8.1.2/32"),
			},
			want: Changes{Added: keys(), Removed: keys(), Updated: keys()},
		},
		{
			name: "removed peer",
			from: []awg.Peer{
				peer(0x01, psk(0x11), "10.8.1.2/32"),
				peer(0x02, psk(0x12), "10.8.1.3/32"),
			},
			to:   []awg.Peer{peer(0x01, psk(0x11), "10.8.1.2/32")},
			want: Changes{Added: keys(), Removed: keys(0x02), Updated: keys()},
		},
		{
			name: "psk changed",
			from: []awg.Peer{peer(0x01, psk(0x11), "10.8.1.2/32")},
			to:   []awg.Peer{peer(0x01, psk(0x21), "10.8.1.2/32")},
			want: Changes{Added: keys(), Removed: keys(), Updated: keys(0x01)},
		},
		{
			name: "kernel peer without psk against a desired psk",
			from: []awg.Peer{peer(0x01, nil, "10.8.1.2/32")},
			to:   []awg.Peer{peer(0x01, psk(0x11), "10.8.1.2/32")},
			want: Changes{Added: keys(), Removed: keys(), Updated: keys(0x01)},
		},
		{
			name: "psk removed",
			from: []awg.Peer{peer(0x01, psk(0x11), "10.8.1.2/32")},
			to:   []awg.Peer{peer(0x01, nil, "10.8.1.2/32")},
			want: Changes{Added: keys(), Removed: keys(), Updated: keys(0x01)},
		},
		{
			name: "allowed ip changed",
			from: []awg.Peer{peer(0x01, psk(0x11), "10.8.1.2/32")},
			to:   []awg.Peer{peer(0x01, psk(0x11), "10.8.1.9/32")},
			want: Changes{Added: keys(), Removed: keys(), Updated: keys(0x01)},
		},
		{
			name: "prefix length changed",
			from: []awg.Peer{peer(0x01, psk(0x11), "10.8.1.0/28")},
			to:   []awg.Peer{peer(0x01, psk(0x11), "10.8.1.0/29")},
			want: Changes{Added: keys(), Removed: keys(), Updated: keys(0x01)},
		},
		{
			name: "two kernel cidrs against one desired cidr",
			from: []awg.Peer{peer(0x01, psk(0x11), "10.8.1.2/32", "10.8.1.3/32")},
			to:   []awg.Peer{peer(0x01, psk(0x11), "10.8.1.2/32")},
			want: Changes{Added: keys(), Removed: keys(), Updated: keys(0x01)},
		},
		{
			name: "kernel peer without allowed ips against one cidr",
			from: []awg.Peer{peer(0x01, psk(0x11))},
			to:   []awg.Peer{peer(0x01, psk(0x11), "10.8.1.2/32")},
			want: Changes{Added: keys(), Removed: keys(), Updated: keys(0x01)},
		},
		{
			name: "allowed ip order counts as a change",
			from: []awg.Peer{peer(0x01, psk(0x11), "10.8.1.3/32", "fd00:8::2/128")},
			to:   []awg.Peer{peer(0x01, psk(0x11), "fd00:8::2/128", "10.8.1.3/32")},
			want: Changes{Added: keys(), Removed: keys(), Updated: keys(0x01)},
		},
		{
			name: "runtime fields do not count as a change",
			from: []awg.Peer{{
				PublicKey:     key(0x01),
				PresharedKey:  psk(0x11),
				Endpoint:      "203.0.113.5:51820",
				AllowedIPs:    []netip.Prefix{netip.MustParsePrefix("10.8.1.2/32")},
				LastHandshake: time.Unix(1790000000, 0).UTC(),
				RxBytes:       100,
				TxBytes:       200,
			}},
			to:   []awg.Peer{peer(0x01, psk(0x11), "10.8.1.2/32")},
			want: Changes{Added: keys(), Removed: keys(), Updated: keys()},
		},
		{
			name: "every list is sorted by raw key bytes",
			from: []awg.Peer{
				peer(0xF0, psk(0x11), "10.8.1.10/32"),
				peer(0x0A, psk(0x11), "10.8.1.11/32"),
				peer(0x80, psk(0x11), "10.8.1.12/32"),
				peer(0x7F, psk(0x11), "10.8.1.13/32"),
				peer(0x05, psk(0x11), "10.8.1.14/32"),
				peer(0xA0, psk(0x11), "10.8.1.15/32"),
			},
			to: []awg.Peer{
				peer(0xFF, psk(0x11), "10.8.1.20/32"),
				peer(0x80, psk(0x22), "10.8.1.12/32"),
				peer(0x01, psk(0x11), "10.8.1.21/32"),
				peer(0x7F, psk(0x22), "10.8.1.13/32"),
				peer(0x03, psk(0x11), "10.8.1.22/32"),
				peer(0xA0, psk(0x11), "10.8.1.15/32"),
				peer(0x05, psk(0x22), "10.8.1.14/32"),
			},
			want: Changes{
				Added:   keys(0x01, 0x03, 0xFF),
				Removed: keys(0x0A, 0xF0),
				Updated: keys(0x05, 0x7F, 0x80),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Diff(tt.from, tt.to)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Diff:\n got %s\nwant %s", describeChanges(got), describeChanges(tt.want))
			}
		})
	}
}

// describeChanges prints the first byte of every key, which is enough to tell the test keys apart.
func describeChanges(c Changes) string {
	short := func(ks []awg.Key) []byte {
		out := make([]byte, 0, len(ks))
		for _, k := range ks {
			out = append(out, k[0])
		}
		return out
	}
	return fmt.Sprintf("added=% x removed=% x updated=% x",
		short(c.Added), short(c.Removed), short(c.Updated))
}
