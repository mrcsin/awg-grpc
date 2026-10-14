package awg

import (
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"time"
)

const redacted = "***"

type Key [32]byte

func (k Key) String() string {
	return base64.StdEncoding.EncodeToString(k[:])
}

// PresharedKey prints as *** for every fmt verb, in slog and as text. The key sits in an
// unexported field, so outside this package only KeyFile returns it.
type PresharedKey struct {
	key [32]byte
}

func NewPresharedKey(raw [32]byte) *PresharedKey {
	return &PresharedKey{key: raw}
}

// KeyFile returns the key in the file format awg reads for preshared-key: base64 and a newline.
func (k PresharedKey) KeyFile() []byte {
	return []byte(base64.StdEncoding.EncodeToString(k.key[:]) + "\n")
}

func (k PresharedKey) Format(f fmt.State, _ rune) {
	_, _ = io.WriteString(f, redacted)
}

func (k PresharedKey) LogValue() slog.Value {
	return slog.StringValue(redacted)
}

func (k PresharedKey) MarshalText() ([]byte, error) {
	return []byte(redacted), nil
}

type Device struct {
	PublicKey  Key // zero when the interface has no private key
	ListenPort uint16
	Peers      []Peer
}

type Peer struct {
	PublicKey     Key
	PresharedKey  *PresharedKey  // nil when the peer has none
	Endpoint      string         // "" when the peer has none
	AllowedIPs    []netip.Prefix // empty when the peer has none
	LastHandshake time.Time      // zero when the peer never completed a handshake
	RxBytes       uint64
	TxBytes       uint64
}

func PrefixStrings(prefixes []netip.Prefix) []string {
	texts := make([]string, 0, len(prefixes))
	for _, p := range prefixes {
		texts = append(texts, p.String())
	}
	return texts
}
