// Package reconcile turns a desired peer list into kernel-side peers, diffs two peer sets and
// builds the awg set command that moves the kernel from one to the other.
package reconcile

import (
	"errors"
	"fmt"
	"net/netip"

	awgv1 "github.com/mrcsin/awg-grpc/gen/awg/v1"
	"github.com/mrcsin/awg-grpc/internal/awg"
)

// MaxPeers is the largest desired list one apply accepts. It keeps one awg set invocation within
// ARG_MAX and the file descriptor limit.
const MaxPeers = 1000

// Interface is the kernel state of an interface that a desired list is validated against.
type Interface struct {
	PublicKey awg.Key
	Addresses []netip.Prefix
}

// ValidationError reports the first violation, in the lowest peer index, of the rules in the
// ApplyPeers comment of proto/awg/v1/awg.proto. The message never holds a preshared key.
type ValidationError struct {
	message string
}

func (e *ValidationError) Error() string {
	return e.message
}

func listError(format string, args ...any) *ValidationError {
	return &ValidationError{message: fmt.Sprintf(format, args...)}
}

func peerError(index int, format string, args ...any) *ValidationError {
	return &ValidationError{message: fmt.Sprintf("peer %d: ", index) + fmt.Sprintf(format, args...)}
}

// Desired converts the request's peer list into kernel-side peers in request order and returns
// the first rule violation as *ValidationError.
func Desired(req *awgv1.ApplyPeersRequest, iface Interface) ([]awg.Peer, error) {
	reqPeers := req.GetPeers()
	if len(reqPeers) == 0 && !req.GetAllowEmpty() {
		return nil, listError("peer list is empty; set allow_empty to remove every peer")
	}
	if len(reqPeers) > MaxPeers {
		return nil, listError("peer list holds %d peers, the limit is %d",
			len(reqPeers), MaxPeers)
	}

	peers := make([]awg.Peer, 0, len(reqPeers))
	firstIndex := make(map[awg.Key]int, len(reqPeers))
	for i, rp := range reqPeers {
		peer, err := convertPeer(i, rp, iface, firstIndex)
		if err != nil {
			return nil, err
		}
		firstIndex[peer.PublicKey] = i
		for j, other := range peers {
			if peer.AllowedIPs[0].Overlaps(other.AllowedIPs[0]) {
				return nil, peerError(i, "%s: allowed_ip %s overlaps %s of peer %d (%s)",
					peer.PublicKey, peer.AllowedIPs[0], other.AllowedIPs[0], j, other.PublicKey)
			}
		}
		peers = append(peers, peer)
	}
	return peers, nil
}

// convertPeer checks rules 2 to 8 for one peer; firstIndex maps the public keys of the peers
// before it to their indexes.
func convertPeer(i int, rp *awgv1.Peer, iface Interface, firstIndex map[awg.Key]int) (awg.Peer, error) {
	pub, err := parseKey(rp.GetPublicKey())
	if err != nil {
		return awg.Peer{}, peerError(i, "public key %v", err)
	}
	if pub == iface.PublicKey {
		return awg.Peer{}, peerError(i, "public key %s equals the interface public key", pub)
	}
	if first, ok := firstIndex[pub]; ok {
		return awg.Peer{}, peerError(i, "public key %s repeats peer %d", pub, first)
	}

	shared, err := parseKey(rp.GetPresharedKey())
	if err != nil {
		return awg.Peer{}, peerError(i, "%s: preshared key %v", pub, err)
	}

	prefix, err := parseAllowedIP(rp.GetAllowedIp())
	if err != nil {
		return awg.Peer{}, peerError(i, "%s: %v", pub, err)
	}
	if !insideAny(prefix, iface.Addresses) {
		return awg.Peer{}, peerError(i, "%s: allowed_ip %s lies outside the interface addresses %v",
			pub, prefix, iface.Addresses)
	}
	for _, addr := range iface.Addresses {
		if prefix.Contains(addr.Addr()) {
			return awg.Peer{}, peerError(i, "%s: allowed_ip %s contains the interface address %s",
				pub, prefix, addr.Addr())
		}
	}

	return awg.Peer{
		PublicKey:    pub,
		PresharedKey: awg.NewPresharedKey(shared),
		AllowedIPs:   []netip.Prefix{prefix},
	}, nil
}

// parseKey accepts exactly 32 bytes that are not all zeros. Its errors never quote the key.
func parseKey(b []byte) (awg.Key, error) {
	var k awg.Key
	if len(b) != len(k) {
		return k, fmt.Errorf("is %d bytes, want %d", len(b), len(k))
	}
	copy(k[:], b)
	if k == (awg.Key{}) {
		return k, errors.New("is all zeros")
	}
	return k, nil
}

// parseAllowedIP accepts an IPv4 CIDR in its masked form. The request string is quoted with %q,
// and the netip error is dropped because it quotes its input unescaped.
func parseAllowedIP(s string) (netip.Prefix, error) {
	prefix, err := netip.ParsePrefix(s)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("allowed_ip %q is not a CIDR", s)
	}
	if !prefix.Addr().Is4() {
		return netip.Prefix{}, fmt.Errorf("allowed_ip %s is not IPv4", prefix)
	}
	if masked := prefix.Masked(); prefix != masked {
		return netip.Prefix{}, fmt.Errorf("allowed_ip %s has host bits set, want %s", prefix, masked)
	}
	return prefix, nil
}

// insideAny reports whether prefix lies inside the network prefix of one of the addresses.
func insideAny(prefix netip.Prefix, addresses []netip.Prefix) bool {
	for _, addr := range addresses {
		network := addr.Masked()
		if network.Bits() <= prefix.Bits() && network.Contains(prefix.Addr()) {
			return true
		}
	}
	return false
}
