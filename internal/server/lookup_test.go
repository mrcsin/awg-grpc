package server

import (
	"net"
	"net/netip"
	"slices"
	"testing"
)

func loopbackName(t *testing.T) string {
	t.Helper()
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Fatalf("listing interfaces: %v", err)
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			return iface.Name
		}
	}
	t.Skip("no loopback interface")
	return ""
}

func TestNetLookup(t *testing.T) {
	tests := []struct {
		name        string
		iface       string
		wantPresent bool
		wantAddr    netip.Prefix
	}{
		{
			name:        "loopback is present with its address",
			iface:       loopbackName(t),
			wantPresent: true,
			wantAddr:    netip.MustParsePrefix("127.0.0.1/8"),
		},
		{name: "missing interface is absent", iface: "awgnone0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addrs, present, err := NetLookup(tt.iface)
			if err != nil {
				t.Fatalf("NetLookup: %v", err)
			}
			if present != tt.wantPresent {
				t.Fatalf("present = %v, want %v", present, tt.wantPresent)
			}
			if tt.wantPresent && !slices.Contains(addrs, tt.wantAddr) {
				t.Errorf("addresses = %v, want to hold %s", addrs, tt.wantAddr)
			}
			if !tt.wantPresent && len(addrs) != 0 {
				t.Errorf("addresses = %v, want none", addrs)
			}
		})
	}
}
