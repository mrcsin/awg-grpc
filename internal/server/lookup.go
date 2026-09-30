package server

import (
	"fmt"
	"net"
	"net/netip"
)

// Lookup reports whether a network interface exists in the container network namespace and
// returns its addresses. An error means the lookup itself failed.
type Lookup func(name string) (addrs []netip.Prefix, present bool, err error)

var _ Lookup = NetLookup

// NetLookup is the production Lookup over the network interfaces of the current namespace.
func NetLookup(name string) ([]netip.Prefix, bool, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, false, fmt.Errorf("listing interfaces: %w", err)
	}
	for _, iface := range ifaces {
		if iface.Name != name {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			return nil, false, fmt.Errorf("reading addresses of %s: %w", name, err)
		}
		prefixes := make([]netip.Prefix, 0, len(addrs))
		for _, a := range addrs {
			ipNet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			prefix, err := netip.ParsePrefix(ipNet.String())
			if err != nil {
				return nil, false, fmt.Errorf("parsing address of %s: %w", name, err)
			}
			prefixes = append(prefixes, prefix)
		}
		return prefixes, true, nil
	}
	return nil, false, nil
}
