package reconcile

import (
	"bytes"
	"net/netip"
	"slices"

	"github.com/mrcsin/awg-grpc/internal/awg"
)

// Changes lists the public keys that differ between two peer sets. Each list is sorted by the
// raw key bytes and is never nil.
type Changes struct {
	Added   []awg.Key
	Removed []awg.Key
	Updated []awg.Key
}

// Diff compares two peer sets keyed by public key. A peer present in both is updated when its
// preshared key or its set of allowed IPs differs; endpoint, handshake and counters are ignored.
func Diff(from, to []awg.Peer) Changes {
	before := byPublicKey(from)
	changes := Changes{Added: []awg.Key{}, Removed: []awg.Key{}, Updated: []awg.Key{}}
	seen := make(map[awg.Key]bool, len(to))
	for _, p := range to {
		seen[p.PublicKey] = true
		old, ok := before[p.PublicKey]
		switch {
		case !ok:
			changes.Added = append(changes.Added, p.PublicKey)
		case !samePresharedKey(old.PresharedKey, p.PresharedKey) ||
			!samePrefixSet(old.AllowedIPs, p.AllowedIPs):
			changes.Updated = append(changes.Updated, p.PublicKey)
		}
	}
	for _, p := range from {
		if !seen[p.PublicKey] {
			changes.Removed = append(changes.Removed, p.PublicKey)
		}
	}

	for _, list := range [][]awg.Key{changes.Added, changes.Removed, changes.Updated} {
		slices.SortFunc(list, func(a, b awg.Key) int { return bytes.Compare(a[:], b[:]) })
	}
	return changes
}

func byPublicKey(peers []awg.Peer) map[awg.Key]awg.Peer {
	m := make(map[awg.Key]awg.Peer, len(peers))
	for _, p := range peers {
		m[p.PublicKey] = p
	}
	return m
}

func samePresharedKey(a, b *awg.PresharedKey) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func samePrefixSet(a, b []netip.Prefix) bool {
	setA := make(map[netip.Prefix]bool, len(a))
	for _, p := range a {
		setA[p] = true
	}
	setB := make(map[netip.Prefix]bool, len(b))
	for _, p := range b {
		if !setA[p] {
			return false
		}
		setB[p] = true
	}
	return len(setA) == len(setB)
}
