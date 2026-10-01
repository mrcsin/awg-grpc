package awg

import (
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"time"
)

// redacted is what a PresharedKey and a redacted stderr show in place of a key.
const redacted = "***"

// Key is a public key. It prints as base64, the form awg reads and writes.
type Key [32]byte

func (k Key) String() string {
	return base64.StdEncoding.EncodeToString(k[:])
}

// PresharedKey prints as *** for every fmt verb, in slog and as text. The key sits in an
// unexported field, so outside this package only KeyFile returns it.
type PresharedKey struct {
	key [32]byte
}

// NewPresharedKey wraps a raw key.
func NewPresharedKey(raw [32]byte) *PresharedKey {
	return &PresharedKey{key: raw}
}

// KeyFile returns the key in the file format awg reads for preshared-key: base64 and a newline.
func (k PresharedKey) KeyFile() []byte {
	return []byte(base64.StdEncoding.EncodeToString(k.key[:]) + "\n")
}

// Format writes *** for every verb.
func (k PresharedKey) Format(f fmt.State, _ rune) {
	_, _ = io.WriteString(f, redacted)
}

// LogValue hides the key from slog.
func (k PresharedKey) LogValue() slog.Value {
	return slog.StringValue(redacted)
}

// MarshalText hides the key from encoding/json and every other text encoder.
func (k PresharedKey) MarshalText() ([]byte, error) {
	return []byte(redacted), nil
}

// Device is the kernel state of one interface as reported by awg show dump.
type Device struct {
	PublicKey  Key // zero when the interface has no private key
	ListenPort uint16
	Peers      []Peer
}

// Peer is one kernel peer as reported by awg show dump.
type Peer struct {
	PublicKey     Key
	PresharedKey  *PresharedKey  // nil when the peer has none
	Endpoint      string         // "" when the peer has none
	AllowedIPs    []netip.Prefix // empty when the peer has none
	LastHandshake time.Time      // zero when the peer never completed a handshake
	RxBytes       uint64
	TxBytes       uint64
}

// PrefixStrings returns the String form of every prefix, in order.
func PrefixStrings(prefixes []netip.Prefix) []string {
	texts := make([]string, 0, len(prefixes))
	for _, p := range prefixes {
		texts = append(texts, p.String())
	}
	return texts
}
